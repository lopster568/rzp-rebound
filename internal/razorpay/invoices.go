package razorpay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Invoice, customer, and list endpoint paths, appended to the base URL.
//
// Unlike the paths in client.go these were walked against live test mode on
// 2026-09-05, one call each, and every response body in invoices_test.go is a
// copy of what came back.
const (
	pathCustomers       = "/customers"
	pathInvoices        = "/invoices"
	pathInvoiceByID     = "/invoices/%s"
	pathInvoiceIssue    = "/invoices/%s/issue"
	pathInvoiceCancel   = "/invoices/%s/cancel"
	pathInvoiceNotify   = "/invoices/%s/notify_by/%s"
	pathPaymentLinkByID = "/payment_links/%s"
)

// Invoice status values, observed on 2026-09-05. An invoice created with
// draft set starts at draft and reaches issued only through the issue call;
// one created without it starts at issued.
const (
	InvoiceStatusDraft     = "draft"
	InvoiceStatusIssued    = "issued"
	InvoiceStatusPartial   = "partially_paid"
	InvoiceStatusPaid      = "paid"
	InvoiceStatusCancelled = "cancelled"
	InvoiceStatusExpired   = "expired"
)

// InvoiceNotifyStatusSent is what sms_status and email_status hold once
// Razorpay has sent the notification. Both fields are null until then, which
// is what makes them the only field on the invoice that separates a notify
// call that was made from one that was not.
const InvoiceNotifyStatusSent = "sent"

// Errors the invoice and customer calls return.
var (
	ErrInvoiceNotFound         = errors.New("razorpay: invoice not found")
	ErrCustomerNameMissing     = errors.New("razorpay: a customer needs a name")
	ErrInvoiceCustomerMissing  = errors.New("razorpay: an invoice needs a customer id or an inline customer")
	ErrInvoiceLineItemsMissing = errors.New("razorpay: an invoice needs at least one line item")
)

// Customer is a Razorpay customer.
type Customer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Contact   string `json:"contact"`
	GSTIN     string `json:"gstin,omitempty"`
	CreatedAt int64  `json:"created_at"`
	Notes     Notes  `json:"notes,omitempty"`
}

// InvoiceCustomer is the customer_details object an invoice carries. It is a
// snapshot Razorpay took when the invoice was created, not a live read of the
// customer, so it can disagree with a customer that has since been edited.
//
// Razorpay sends each of the three contact fields twice, once bare and once
// with a customer_ prefix, with the same value in both on 2026-09-05. Only the
// bare spelling is read here; a divergence between the two would be a Razorpay
// change worth noticing rather than something to silently pick a side on.
type InvoiceCustomer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Contact string `json:"contact"`
	GSTIN   string `json:"gstin,omitempty"`
}

// InvoiceLineItem is one line on an invoice, as it comes back. Amounts are in
// paise.
type InvoiceLineItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	AmountPaise   int64  `json:"amount"`
	UnitAmount    int64  `json:"unit_amount"`
	GrossAmount   int64  `json:"gross_amount"`
	TaxAmount     int64  `json:"tax_amount"`
	TaxableAmount int64  `json:"taxable_amount"`
	NetAmount     int64  `json:"net_amount"`
	Currency      string `json:"currency"`
	Quantity      int64  `json:"quantity"`
}

// Invoice is a Razorpay invoice. Amounts are in paise, and every timestamp is
// a Unix second.
//
// Several fields are null on a draft and hold a value once the invoice is
// issued: OrderID, ShortURL, IssuedAt, AmountPaid, and AmountDue. Decoding
// null into an int64 or a string leaves the zero value rather than failing, so
// a caller reads a draft as an invoice with no order behind it, which is what
// it is. Confirmed on 2026-09-05 against a draft and the same invoice after
// the issue call.
type Invoice struct {
	ID              string            `json:"id"`
	InvoiceNumber   string            `json:"invoice_number,omitempty"`
	Receipt         string            `json:"receipt,omitempty"`
	CustomerID      string            `json:"customer_id"`
	CustomerDetails InvoiceCustomer   `json:"customer_details"`
	OrderID         string            `json:"order_id"`
	PaymentID       string            `json:"payment_id,omitempty"`
	LineItems       []InvoiceLineItem `json:"line_items,omitempty"`
	Status          string            `json:"status"`
	Type            string            `json:"type,omitempty"`
	Description     string            `json:"description,omitempty"`
	ShortURL        string            `json:"short_url"`
	Currency        string            `json:"currency"`
	AmountPaise     int64             `json:"amount"`
	AmountPaid      int64             `json:"amount_paid"`
	AmountDue       int64             `json:"amount_due"`
	GrossAmount     int64             `json:"gross_amount"`
	TaxAmount       int64             `json:"tax_amount"`
	TaxableAmount   int64             `json:"taxable_amount"`
	PartialPayment  bool              `json:"partial_payment"`
	// SMSStatus and EmailStatus are null until Razorpay sends the
	// notification and InvoiceNotifyStatusSent afterwards.
	SMSStatus   string `json:"sms_status"`
	EmailStatus string `json:"email_status"`
	Date        int64  `json:"date"`
	IssuedAt    int64  `json:"issued_at"`
	PaidAt      int64  `json:"paid_at"`
	CancelledAt int64  `json:"cancelled_at"`
	ExpiredAt   int64  `json:"expired_at"`
	ExpireBy    int64  `json:"expire_by"`
	CreatedAt   int64  `json:"created_at"`
	Notes       Notes  `json:"notes,omitempty"`
}

// PaymentLinkNotify is the nested notify object on a payment link, reporting
// which media Razorpay was asked to send it over.
type PaymentLinkNotify struct {
	SMS      bool `json:"sms"`
	Email    bool `json:"email"`
	WhatsApp bool `json:"whatsapp"`
}

// PaymentLinkCustomer is the contact a payment link was created for.
//
// It has its own decoder for the reason Notes does: Razorpay does not always
// send it as an object. The create and fetch responses carry an object, and
// the list response carries an empty JSON array for a link created with no
// contact on it. Decoding straight into a struct therefore failed the whole
// list for exactly the links that had nothing in this field. Observed on
// 2026-09-05, on the same account, minutes apart.
type PaymentLinkCustomer struct {
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
	Contact string `json:"contact,omitempty"`
}

// UnmarshalJSON accepts an object, an empty array, or null.
//
// A non-empty array stays an error, on the same reasoning as Notes: it is not
// an object with a different spelling, and dropping its contents would lose
// the contact the link was sent to.
func (c *PaymentLinkCustomer) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*c = PaymentLinkCustomer{}
		return nil
	}
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return fmt.Errorf("razorpay: decode the payment link customer: %w", err)
		}
		if len(items) != 0 {
			return fmt.Errorf("razorpay: the payment link customer arrived as an array of %d item(s), which is not an object", len(items))
		}
		*c = PaymentLinkCustomer{}
		return nil
	}

	// The alias drops the method set, so this unmarshal does not call back
	// into this function.
	type plainCustomer PaymentLinkCustomer
	var out plainCustomer
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return fmt.Errorf("razorpay: decode the payment link customer: %w", err)
	}
	*c = PaymentLinkCustomer(out)
	return nil
}

// PaymentLinkReminders is the reminder state on a payment link. It carries the
// same array-or-object hazard as PaymentLinkCustomer: an object with a status
// on a fetch, an empty array on a list. Observed on 2026-09-05, where the
// status was failed for a link whose contact details test mode would not send
// to.
type PaymentLinkReminders struct {
	Status string `json:"status,omitempty"`
}

// UnmarshalJSON accepts an object, an empty array, or null.
func (r *PaymentLinkReminders) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*r = PaymentLinkReminders{}
		return nil
	}
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return fmt.Errorf("razorpay: decode the payment link reminders: %w", err)
		}
		if len(items) != 0 {
			return fmt.Errorf("razorpay: the payment link reminders arrived as an array of %d item(s), which is not an object", len(items))
		}
		*r = PaymentLinkReminders{}
		return nil
	}

	type plainReminders PaymentLinkReminders
	var out plainReminders
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return fmt.Errorf("razorpay: decode the payment link reminders: %w", err)
	}
	*r = PaymentLinkReminders(out)
	return nil
}

// PaymentLinkDetail is the full payment link as Razorpay sends it, confirmed
// on 2026-09-05.
//
// It is a second type rather than more fields on PaymentLink because
// PaymentLink is part of the Port surface and this package is not the place
// that decides what that surface holds. The overlap is intentional: a caller
// that only needs the id, the URL, and the status keeps using PaymentLink, and
// one that has to tell a partly paid link from an unpaid one needs AmountPaid,
// which only lives here.
type PaymentLinkDetail struct {
	ID             string               `json:"id"`
	ShortURL       string               `json:"short_url"`
	Status         string               `json:"status"`
	AmountPaise    int64                `json:"amount"`
	AmountPaid     int64                `json:"amount_paid"`
	Currency       string               `json:"currency"`
	Description    string               `json:"description,omitempty"`
	ReferenceID    string               `json:"reference_id,omitempty"`
	Customer       PaymentLinkCustomer  `json:"customer"`
	Notify         PaymentLinkNotify    `json:"notify"`
	Reminders      PaymentLinkReminders `json:"reminders"`
	ReminderEnable bool                 `json:"reminder_enable"`
	AcceptPartial  bool                 `json:"accept_partial"`
	UPILink        bool                 `json:"upi_link"`
	WhatsAppLink   bool                 `json:"whatsapp_link"`
	CreatedAt      int64                `json:"created_at"`
	UpdatedAt      int64                `json:"updated_at"`
	CancelledAt    int64                `json:"cancelled_at"`
	ExpiredAt      int64                `json:"expired_at"`
	ExpireBy       int64                `json:"expire_by"`
	Notes          Notes                `json:"notes,omitempty"`
}

// ListOptions narrows a list call. A zero value asks for whatever page
// Razorpay answers with by default, which was ten items on 2026-09-05.
//
// Razorpay caps Count at 100 and refuses a larger one. Nothing here clamps it:
// a silently shrunk page would make a caller that asked for 500 believe it had
// seen everything.
type ListOptions struct {
	// Count is how many records to return. Zero leaves it to Razorpay.
	Count int
	// Skip is how many records to step over first, which is how the second
	// and later pages are read.
	Skip int
	// From and To bound created_at, in Unix seconds. Zero means unbounded.
	From int64
	To   int64
}

// query renders the options as a query string, empty when nothing is set.
func (o ListOptions) query() string {
	values := url.Values{}
	if o.Count > 0 {
		values.Set("count", strconv.Itoa(o.Count))
	}
	if o.Skip > 0 {
		values.Set("skip", strconv.Itoa(o.Skip))
	}
	if o.From > 0 {
		values.Set("from", strconv.FormatInt(o.From, 10))
	}
	if o.To > 0 {
		values.Set("to", strconv.FormatInt(o.To, 10))
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// orderCollection is the list envelope the orders endpoint answers with,
// confirmed on 2026-09-05.
type orderCollection struct {
	Count int     `json:"count"`
	Items []Order `json:"items"`
}

// invoiceCollection is the same envelope from the invoices endpoint.
type invoiceCollection struct {
	Count int       `json:"count"`
	Items []Invoice `json:"items"`
}

// paymentLinkCollection is the envelope the payment links endpoint answers
// with, and it is not the one every other list uses.
//
// Orders and invoices come back as {"entity":"collection","count":n,"items":
// [...]}. Payment links come back as {"payment_links":[...]} with no entity
// and no count at all. Confirmed on 2026-09-05. A caller that wants a total
// has to count the slice.
type paymentLinkCollection struct {
	PaymentLinks []PaymentLinkDetail `json:"payment_links"`
}

// createCustomerBody is the POST body for the customers endpoint.
//
// FailExisting is a string rather than a bool because that is how Razorpay
// documents it, and it is the one field here the 2026-09-05 walk did not
// exercise: the probe created a customer that did not exist yet. It is sent
// explicitly on every call so the behaviour does not depend on a gateway
// default this package has not observed.
type createCustomerBody struct {
	Name         string            `json:"name"`
	Email        string            `json:"email,omitempty"`
	Contact      string            `json:"contact,omitempty"`
	FailExisting string            `json:"fail_existing"`
	Notes        map[string]string `json:"notes,omitempty"`
}

// CreateCustomerRequest is the input to CreateCustomer.
type CreateCustomerRequest struct {
	// Name is required. Razorpay refuses a customer without one.
	Name string
	// Email and Contact are both optional to Razorpay, and an invoice sent to
	// a customer that has neither has nowhere to go.
	Email   string
	Contact string
	Notes   map[string]string
	// FailExisting decides what happens when a customer with these contact
	// details already exists. False, the default here, asks Razorpay to return
	// the existing customer, which is what a recovery run wants: it can be
	// re-run over the same batch without a duplicate customer per attempt.
	FailExisting bool
}

// InlineCustomer is the customer object an invoice can carry instead of a
// customer id. Razorpay creates or matches a customer from it.
type InlineCustomer struct {
	Name    string
	Email   string
	Contact string
}

// CreateInvoiceLineItem is one line on a new invoice. AmountPaise is in paise
// and is per unit: Razorpay multiplies it by Quantity.
type CreateInvoiceLineItem struct {
	Name        string
	Description string
	AmountPaise int64
	Currency    string
	// Quantity defaults to 1 when it is not positive.
	Quantity int64
}

// CreateInvoiceRequest is the input to CreateInvoice.
type CreateInvoiceRequest struct {
	// CustomerID names an existing customer. Customer supplies one inline.
	// Exactly one of the two is needed; CustomerID wins if both are set.
	CustomerID string
	Customer   *InlineCustomer
	LineItems  []CreateInvoiceLineItem
	// Draft holds the invoice at status draft instead of issuing it. A draft
	// has no order and no short URL behind it until IssueInvoice is called.
	Draft          bool
	Description    string
	Receipt        string
	Currency       string
	PartialPayment bool
	// ExpireBy is a Unix second. Razorpay refuses one less than fifteen
	// minutes out.
	ExpireBy    int64
	NotifySMS   bool
	NotifyEmail bool
	Notes       map[string]string
}

// createInvoiceLineItemBody is one line item as the API takes it.
type createInvoiceLineItemBody struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency,omitempty"`
	Quantity    int64  `json:"quantity"`
}

// createInvoiceCustomerBody is the inline customer object.
type createInvoiceCustomerBody struct {
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
	Contact string `json:"contact,omitempty"`
}

// createInvoiceBody is the POST body for the invoices endpoint.
//
// Three of these fields are not the Go type their name suggests, and all three
// are Razorpay's spelling rather than a choice made here:
//
//   - type is required and is the literal string invoice. The same endpoint
//     also creates links and e-invoices, and omitting it is a 400.
//   - draft is the string "1" or "0", not a boolean. A JSON true is rejected.
//   - sms_notify and email_notify are the integers 1 and 0, not booleans.
//
// The draft path is the one this package uses: it creates the invoice, lets a
// caller check it, and issues it in a second call, so a half-built invoice is
// never sent to a customer.
type createInvoiceBody struct {
	Type           string                      `json:"type"`
	Draft          string                      `json:"draft"`
	CustomerID     string                      `json:"customer_id,omitempty"`
	Customer       *createInvoiceCustomerBody  `json:"customer,omitempty"`
	LineItems      []createInvoiceLineItemBody `json:"line_items"`
	Description    string                      `json:"description,omitempty"`
	Receipt        string                      `json:"receipt,omitempty"`
	Currency       string                      `json:"currency,omitempty"`
	PartialPayment bool                        `json:"partial_payment"`
	ExpireBy       int64                       `json:"expire_by,omitempty"`
	SMSNotify      int                         `json:"sms_notify"`
	EmailNotify    int                         `json:"email_notify"`
	Notes          map[string]string           `json:"notes,omitempty"`
}

// boolFlag renders a Razorpay integer flag.
func boolFlag(b bool) int {
	if b {
		return 1
	}
	return 0
}

// boolString renders a Razorpay string flag, which is the spelling draft and
// fail_existing use.
func boolString(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// CreateCustomer creates or matches a customer.
//
// With FailExisting left false this is safe to re-run over the same batch:
// Razorpay answers with the customer that already holds those contact details
// rather than making a second one.
func (c *Client) CreateCustomer(ctx context.Context, req CreateCustomerRequest) (Customer, error) {
	if req.Name == "" {
		return Customer{}, ErrCustomerNameMissing
	}

	var out Customer
	err := c.do(ctx, http.MethodPost, pathCustomers, createCustomerBody{
		Name:         req.Name,
		Email:        req.Email,
		Contact:      req.Contact,
		FailExisting: boolString(req.FailExisting),
		Notes:        req.Notes,
	}, &out)
	if err != nil {
		return Customer{}, err
	}
	return out, nil
}

// CreateInvoice posts a new invoice.
//
// With Draft set the invoice comes back at status draft with a null order_id
// and a null short_url: there is nothing to pay yet and nothing to send.
// IssueInvoice is what mints both. Without Draft the invoice is issued on
// creation and carries an order and a short URL straight away.
func (c *Client) CreateInvoice(ctx context.Context, req CreateInvoiceRequest) (Invoice, error) {
	if req.CustomerID == "" && req.Customer == nil {
		return Invoice{}, ErrInvoiceCustomerMissing
	}
	if len(req.LineItems) == 0 {
		return Invoice{}, ErrInvoiceLineItemsMissing
	}

	items := make([]createInvoiceLineItemBody, 0, len(req.LineItems))
	for i, item := range req.LineItems {
		if item.AmountPaise <= 0 {
			return Invoice{}, fmt.Errorf("%w: line item %d is %d paise", ErrAmountNotPositive, i, item.AmountPaise)
		}
		quantity := item.Quantity
		if quantity <= 0 {
			quantity = 1
		}
		items = append(items, createInvoiceLineItemBody{
			Name:        item.Name,
			Description: item.Description,
			Amount:      item.AmountPaise,
			Currency:    item.Currency,
			Quantity:    quantity,
		})
	}

	body := createInvoiceBody{
		Type:           "invoice",
		Draft:          boolString(req.Draft),
		CustomerID:     req.CustomerID,
		LineItems:      items,
		Description:    req.Description,
		Receipt:        req.Receipt,
		Currency:       req.Currency,
		PartialPayment: req.PartialPayment,
		ExpireBy:       req.ExpireBy,
		SMSNotify:      boolFlag(req.NotifySMS),
		EmailNotify:    boolFlag(req.NotifyEmail),
		Notes:          req.Notes,
	}
	// The two are mutually exclusive to Razorpay: sending both is a 400, so
	// the id wins and the inline object is dropped rather than failing a call
	// that named the customer twice and agreed with itself.
	if req.CustomerID == "" && req.Customer != nil {
		body.Customer = &createInvoiceCustomerBody{
			Name:    req.Customer.Name,
			Email:   req.Customer.Email,
			Contact: req.Customer.Contact,
		}
	}

	var out Invoice
	if err := c.do(ctx, http.MethodPost, pathInvoices, body, &out); err != nil {
		return Invoice{}, err
	}
	return out, nil
}

// IssueInvoice moves a draft invoice to issued.
//
// This is the call that mints the order the invoice is paid against and the
// short URL a customer opens: both are null on the draft and both are set in
// the response to this. Confirmed on 2026-09-05.
func (c *Client) IssueInvoice(ctx context.Context, invoiceID string) (Invoice, error) {
	if invoiceID == "" {
		return Invoice{}, fmt.Errorf("%w: no invoice id given", ErrInvoiceNotFound)
	}

	var out Invoice
	path := fmt.Sprintf(pathInvoiceIssue, url.PathEscape(invoiceID))
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return Invoice{}, mapNotFound(err, ErrInvoiceNotFound, invoiceID)
	}
	return out, nil
}

// FetchInvoice reads one invoice.
//
// It is the only way to see whether a notification was sent: email_status and
// sms_status are on the invoice and nowhere else, and NotifyInvoice reports
// only that its own call was accepted.
func (c *Client) FetchInvoice(ctx context.Context, invoiceID string) (Invoice, error) {
	if invoiceID == "" {
		return Invoice{}, fmt.Errorf("%w: no invoice id given", ErrInvoiceNotFound)
	}

	var out Invoice
	path := fmt.Sprintf(pathInvoiceByID, url.PathEscape(invoiceID))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Invoice{}, mapNotFound(err, ErrInvoiceNotFound, invoiceID)
	}
	return out, nil
}

// ListInvoices reads a page of invoices.
func (c *Client) ListInvoices(ctx context.Context, opts ListOptions) ([]Invoice, error) {
	var out invoiceCollection
	if err := c.do(ctx, http.MethodGet, pathInvoices+opts.query(), nil, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		return []Invoice{}, nil
	}
	return out.Items, nil
}

// NotifyInvoice asks Razorpay to send an issued invoice over one medium.
//
// The receipt reports that the API call succeeded. It says nothing about
// whether anything was delivered or read, which is why the caller that wants
// to know reads EmailStatus or SMSStatus off a later FetchInvoice instead.
//
// The receipt is a NotifyReceipt, whose LinkID field holds the invoice id
// here. It is reused rather than duplicated so a caller that records both
// kinds of send has one shape to record.
func (c *Client) NotifyInvoice(ctx context.Context, invoiceID, medium string) (NotifyReceipt, error) {
	if medium != MediumSMS && medium != MediumEmail {
		return NotifyReceipt{}, fmt.Errorf("%w: %q", ErrUnsupportedMedium, medium)
	}
	if invoiceID == "" {
		return NotifyReceipt{}, fmt.Errorf("%w: no invoice id given", ErrInvoiceNotFound)
	}

	var out notifyResponse
	path := fmt.Sprintf(pathInvoiceNotify, url.PathEscape(invoiceID), url.PathEscape(medium))
	// Tolerates an empty 2xx body for the reason the payment link resend
	// does. On 2026-09-05 the endpoint answered {"success":true}, and reading
	// that field makes a 200 that reports a refusal visible instead of being
	// counted as an acceptance.
	if err := c.doWith(ctx, http.MethodPost, path, nil, &out, true); err != nil {
		return NotifyReceipt{}, mapNotFound(err, ErrInvoiceNotFound, invoiceID)
	}

	accepted := true
	if out.Success != nil {
		accepted = *out.Success
	}

	return NotifyReceipt{
		LinkID:      invoiceID,
		Medium:      medium,
		Accepted:    accepted,
		RequestedAt: c.clock.Now(),
	}, nil
}

// CancelInvoice cancels an issued invoice.
//
// The invoice comes back at status cancelled with cancelled_at set, and the
// short URL stops being payable. A paid invoice cannot be cancelled and
// Razorpay answers that with a 400.
func (c *Client) CancelInvoice(ctx context.Context, invoiceID string) (Invoice, error) {
	if invoiceID == "" {
		return Invoice{}, fmt.Errorf("%w: no invoice id given", ErrInvoiceNotFound)
	}

	var out Invoice
	path := fmt.Sprintf(pathInvoiceCancel, url.PathEscape(invoiceID))
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return Invoice{}, mapNotFound(err, ErrInvoiceNotFound, invoiceID)
	}
	return out, nil
}

// FetchPaymentLink reads one payment link, with the fields CreatePaymentLink's
// narrower return type leaves out. AmountPaid is the one that matters to a
// recovery run: it is how a partly paid link is told from an unpaid one.
func (c *Client) FetchPaymentLink(ctx context.Context, linkID string) (PaymentLinkDetail, error) {
	if linkID == "" {
		return PaymentLinkDetail{}, fmt.Errorf("%w: no link id given", ErrPaymentLinkNotFound)
	}

	var out PaymentLinkDetail
	path := fmt.Sprintf(pathPaymentLinkByID, url.PathEscape(linkID))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return PaymentLinkDetail{}, mapNotFound(err, ErrPaymentLinkNotFound, linkID)
	}
	return out, nil
}

// ListPaymentLinks reads the account's payment links.
//
// ListOptions is accepted for the same reason the other two lists take it, and
// it is the least tested of the three: the 2026-09-05 walk read this endpoint
// with no parameters at all, so what Razorpay does with count and skip here is
// documented rather than observed.
func (c *Client) ListPaymentLinks(ctx context.Context, opts ListOptions) ([]PaymentLinkDetail, error) {
	var out paymentLinkCollection
	if err := c.do(ctx, http.MethodGet, pathPaymentLinks+opts.query(), nil, &out); err != nil {
		return nil, err
	}
	if out.PaymentLinks == nil {
		return []PaymentLinkDetail{}, nil
	}
	return out.PaymentLinks, nil
}

// ListOrders reads a page of orders, newest first.
func (c *Client) ListOrders(ctx context.Context, opts ListOptions) ([]Order, error) {
	var out orderCollection
	if err := c.do(ctx, http.MethodGet, pathOrders+opts.query(), nil, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		return []Order{}, nil
	}
	return out.Items, nil
}
