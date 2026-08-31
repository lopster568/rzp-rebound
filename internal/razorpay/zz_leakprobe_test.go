package razorpay_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// PROBE 1: truncate-then-redact lets a partial secret through APIError.Body.
func TestProbeTruncationBeatsRedaction(t *testing.T) {
	pad := strings.Repeat("x", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		// secret begins near byte 500 so truncation cuts it in half
		_, _ = w.Write([]byte(`{"d":"` + pad + testKeySecret + `"}`))
	}))
	defer srv.Close()

	c, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID: testKeyID, KeySecret: testKeySecret,
		BaseURL: srv.URL + "/v1", Clock: clock.NewFake(fakeStart),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.FetchOrder(context.Background(), "order_probe000001")
	msg := err.Error()
	t.Logf("ERROR MESSAGE TAIL: %q", msg[len(msg)-80:])
	// how many leading chars of the secret survived?
	for n := len(testKeySecret); n > 0; n-- {
		if strings.Contains(msg, testKeySecret[:n]) {
			t.Logf("LEAKED %d of %d secret chars verbatim: %q", n, len(testKeySecret), testKeySecret[:n])
			break
		}
	}
}

// PROBE 2: captureResponse writes a valid-JSON body without redaction.
func TestProbeCaptureSkipsRedactionForJSONBodies(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte(testKeyID + ":" + testKeySecret))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"order_probe000002","status":"created",` +
			`"notes":{"echo":"Basic ` + token + ` key ` + testKeySecret + `","card":"4111111111111111"}}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	c, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID: testKeyID, KeySecret: testKeySecret,
		BaseURL: srv.URL + "/v1", Clock: clock.NewFake(fakeStart), RawCapture: &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.FetchOrder(context.Background(), "order_probe000002"); err != nil {
		t.Fatal(err)
	}
	line := buf.String()
	t.Logf("CAPTURE LINE: %s", line)
	if strings.Contains(line, testKeySecret) {
		t.Logf("LEAK: key secret verbatim in the capture stream")
	}
	if strings.Contains(line, token) {
		t.Logf("LEAK: base64 basic-auth token verbatim in the capture stream")
	}
	if strings.Contains(line, "4111111111111111") {
		t.Logf("LEAK: card number verbatim in the capture stream")
	}
}
