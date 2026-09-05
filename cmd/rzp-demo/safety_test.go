package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/intervene"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// The tests in this file are the no-live-call guarantee, held as assertions
// over this package's own syntax tree rather than as a paragraph in a doc
// comment. A paragraph cannot fail a build.
//
// The guarantee is not that internal/razorpay is absent from the import graph.
// It cannot be: internal/intervene declares its Gateway interface in terms of
// that package's types and asserts razorpay.Client satisfies it, so anything
// that runs the intervention engine links the client's code. The guarantee is
// narrower and it is enough: this binary never builds one, never reads a
// credential, and never makes an outbound request.

// packageFiles parses every non-test Go file in this directory.
//
// Test files are excluded on purpose. A test may construct whatever it needs to
// assert something; what ships in the binary is the non-test set.
func packageFiles(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse this package: %v", err)
	}
	files := map[string]*ast.File{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			files[name] = file
		}
	}
	if len(files) == 0 {
		t.Fatal("parsed no files, so this test is asserting nothing")
	}
	return fset, files
}

// selectorNames walks every file and reports each qualified call it makes, as
// "pkg.Name", with the position it was made at.
func selectorNames(files map[string]*ast.File) map[string][]ast.Node {
	out := map[string][]ast.Node{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			key := ident.Name + "." + sel.Sel.Name
			out[key] = append(out[key], sel)
			return true
		})
	}
	return out
}

func TestTheOnlyEnvironmentVariableThisBinaryReadsIsPORT(t *testing.T) {
	fset, files := packageFiles(t)

	// Every spelling of an environment read. os.Environ is here too: a binary
	// that read the whole environment could find a key in it.
	readers := map[string]bool{
		"os.Getenv":      true,
		"os.LookupEnv":   true,
		"os.Environ":     true,
		"os.ExpandEnv":   true,
		"syscall.Getenv": true,
	}

	found := 0
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			name := ident.Name + "." + sel.Sel.Name
			if !readers[name] {
				return true
			}

			found++
			pos := fset.Position(call.Pos())
			if len(call.Args) != 1 {
				t.Errorf("%s: %s takes no single literal argument, so this test cannot tell what it reads", pos, name)
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: %s reads a name this test cannot see; the environment read has to be a literal", pos, name)
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: cannot read the argument to %s: %v", pos, name, err)
				return true
			}
			if value != "PORT" {
				t.Errorf("%s: this binary reads the environment variable %q. PORT is the only one it may read", pos, value)
			}
			return true
		})
	}

	if found == 0 {
		t.Error("no environment read at all, which means listenPort stopped reading PORT and this test stopped asserting anything")
	}
}

func TestNothingHereConstructsALiveRazorpayClientOrLoadsCredentials(t *testing.T) {
	fset, files := packageFiles(t)
	selectors := selectorNames(files)

	// Every constructor and loader that could put a credential, or a client
	// holding one, into this process.
	banned := map[string]string{
		"razorpay.NewClient": "builds an HTTP client with an Authorization header on it",
		"razorpay.New":       "builds a live client",
		"razorpay.NewFake":   "the fake belongs to the test suite, and this package has its own simGateway",
		"config.Load":        "loads Razorpay keys",
		"config.FromEnv":     "loads Razorpay keys",
		"config.New":         "loads Razorpay keys",
	}
	for name, why := range banned {
		for _, node := range selectors[name] {
			t.Errorf("%s: %s is called here, and it %s", fset.Position(node.Pos()), name, why)
		}
	}

	// The import is the cheaper half of the same check: internal/config exists
	// only to load keys, so importing it at all is the mistake.
	for path, file := range files {
		for _, imp := range file.Imports {
			value, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if strings.HasSuffix(value, "/internal/config") {
				t.Errorf("%s imports %s, which is the package that loads Razorpay keys", filepath.Base(path), value)
			}
		}
	}
}

func TestNothingHereMakesAnOutboundRequest(t *testing.T) {
	fset, files := packageFiles(t)
	selectors := selectorNames(files)

	// net/http is in this package as a server. These are the client spellings,
	// plus the two lower-level dial paths that would bypass them.
	banned := []string{
		"http.Get", "http.Post", "http.PostForm", "http.Head",
		"http.NewRequest", "http.NewRequestWithContext",
		"http.DefaultClient", "http.DefaultTransport",
		"net.Dial", "net.DialTimeout", "tls.Dial",
	}
	for _, name := range banned {
		for _, node := range selectors[name] {
			t.Errorf("%s: %s is an outbound call, and this binary makes none", fset.Position(node.Pos()), name)
		}
	}

	// A composite literal building a client is the other way to get one.
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == "http" && (sel.Sel.Name == "Client" || sel.Sel.Name == "Transport") {
				t.Errorf("%s: an http.%s is built here, and this binary makes no outbound request",
					fset.Position(lit.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

func TestTheSimulatedGatewayIsTheOnlyGatewayAndHoldsNoAddress(t *testing.T) {
	// The interface assertion is in gateway.go and this is the runtime half of
	// it: the value the intervention engine would be handed is this one.
	var g intervene.Gateway = newSimGateway(mustFixtureBook(t))
	if _, ok := g.(*simGateway); !ok {
		t.Fatalf("the demo gateway is %T, want *simGateway", g)
	}

	// A minted link has to point at a host that resolves nowhere. This page is
	// public, and a plausible payment URL on it is the one thing here that
	// could cost a stranger money.
	sim := g.(*simGateway)
	link, err := sim.CreatePaymentLink(t.Context(), createLinkForTest())
	if err != nil {
		t.Fatalf("mint a link: %v", err)
	}
	if !strings.HasPrefix(link.ShortURL, "https://pay.invalid/") {
		t.Errorf("a minted link points at %q, and it has to point at pay.invalid", link.ShortURL)
	}

	// Two gateways built from the same book mint the same ids, which is what
	// makes the page reproducible for two people looking at it together.
	other := newSimGateway(mustFixtureBook(t))
	again, err := other.CreatePaymentLink(t.Context(), createLinkForTest())
	if err != nil {
		t.Fatalf("mint a link from a second gateway: %v", err)
	}
	if again.ID != link.ID {
		t.Errorf("two gateways minted %s and %s, want the same id", link.ID, again.ID)
	}
}

// mustFixtureBook decodes the embedded fixture manifest, which is the book both
// the run view and the gateway are built from.
func mustFixtureBook(t *testing.T) seed.Manifest {
	t.Helper()

	b, err := assets.ReadFile("testdata/fixture-manifest.json")
	if err != nil {
		t.Fatalf("read the fixture manifest: %v", err)
	}
	book, err := decodeManifest(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode the fixture manifest: %v", err)
	}
	if len(book.Items) == 0 {
		t.Fatal("the fixture book is empty")
	}
	return book
}

// createLinkForTest is one link request, fixed, so two gateways asked the same
// question can be compared.
func createLinkForTest() razorpay.CreatePaymentLinkRequest {
	return razorpay.CreatePaymentLinkRequest{
		AmountPaise: 107500,
		Currency:    "INR",
		Description: intervene.DefaultLinkDescription,
		ReferenceID: "ri_testreference",
	}
}

func TestRupeesRendersTheWayTheRunLogsRenderIt(t *testing.T) {
	cases := []struct {
		paise int64
		want  string
	}{
		{418300, "INR 4183.00"},
		{-418300, "INR -4183.00"},
		{0, "INR 0.00"},
		{5, "INR 0.05"},
		{4994500, "INR 49945.00"},
		{1500000, "INR 15000.00"},
	}
	for _, c := range cases {
		if got := rupees(c.paise); got != c.want {
			t.Errorf("rupees(%d) = %q, want %q", c.paise, got, c.want)
		}
	}
}
