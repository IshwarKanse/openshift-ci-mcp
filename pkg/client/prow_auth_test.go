package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testTokenEnv = "TEST_PROW_TOKEN"
const secretToken = "sha256~super-secret-token"

// authServer records the Authorization header of every request.
func authServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func privateDeck(srv *httptest.Server) Deck {
	return Deck{Name: "private", DeckURL: srv.URL, GCSWebURL: srv.URL, Buckets: []string{"bkt"}, TokenEnv: testTokenEnv}
}

func TestProwAuth_SendsTokenToDeckHosts(t *testing.T) {
	t.Setenv(testTokenEnv, " "+secretToken+"\n")
	srv, seen := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	c := NewProw(nil, srv.Client())
	obj, err := c.Open(context.Background(), privateDeck(srv), "logs/j/1/build-log.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	obj.Body.Close()
	if got := (*seen)[0]; got != "Bearer "+secretToken {
		t.Errorf("Authorization = %q", got)
	}
}

func TestProwAuth_NoTokenForPublicDeck(t *testing.T) {
	t.Setenv(DefaultTokenEnv, secretToken)
	srv, seen := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	c := NewProw(nil, srv.Client())
	obj, err := c.Open(context.Background(), testDeck(srv), "logs/j/1/build-log.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	obj.Body.Close()
	if got := (*seen)[0]; got != "" {
		t.Errorf("public deck got Authorization %q", got)
	}
}

func TestProwAuth_MissingToken(t *testing.T) {
	t.Setenv(testTokenEnv, "")
	srv, seen := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	_, err := NewProw(nil, srv.Client()).Open(context.Background(), privateDeck(srv), "logs/j/1/build-log.txt")
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.Rejected {
		t.Fatalf("expected a missing-token AuthError, got %v", err)
	}
	if !strings.Contains(err.Error(), testTokenEnv) {
		t.Errorf("error should name the variable: %v", err)
	}
	if len(*seen) != 0 {
		t.Error("no request should be sent without a token")
	}
}

func TestProwAuth_RejectedToken(t *testing.T) {
	t.Setenv(testTokenEnv, secretToken)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv, _ := authServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden: "+r.Header.Get("Authorization"), status)
		})
		_, err := NewProw(nil, srv.Client()).Open(context.Background(), privateDeck(srv), "logs/j/1/build-log.txt")
		var authErr *AuthError
		if !errors.As(err, &authErr) || !authErr.Rejected {
			t.Fatalf("status %d: expected a rejected-token AuthError, got %v", status, err)
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Errorf("error leaks the token: %v", err)
		}
	}
}

func TestProwAuth_NotFoundIsNotAnAuthError(t *testing.T) {
	t.Setenv(testTokenEnv, secretToken)
	srv, _ := authServer(t, http.NotFound)
	_, err := NewProw(nil, srv.Client()).Open(context.Background(), privateDeck(srv), "logs/j/1/missing")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404 HTTPError, got %v", err)
	}
}

func TestProwAuth_RedirectToOtherHostDropsToken(t *testing.T) {
	t.Setenv(testTokenEnv, secretToken)
	other, otherSeen := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>login</html>")
	})
	// 127.0.0.1 and localhost are different hosts for the same listener,
	// so the redirect leaves the deck's hosts.
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	srv, _ := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherURL+"/oauth/start", http.StatusFound)
	})
	_, err := NewProw(nil, srv.Client()).Open(context.Background(), privateDeck(srv), "logs/j/1/build-log.txt")
	if len(*otherSeen) != 0 {
		t.Errorf("redirect off the deck was followed: %v", *otherSeen)
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || !authErr.Rejected {
		t.Errorf("a redirect off the deck should be reported as a rejected token, got %v", err)
	}
}

func TestProwAuth_RedirectToExtraHostIsAllowed(t *testing.T) {
	t.Setenv(testTokenEnv, secretToken)
	storage, storageSeen := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "log line")
	})
	storageURL := strings.Replace(storage.URL, "127.0.0.1", "localhost", 1)
	srv, _ := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storageURL+r.URL.Path, http.StatusMovedPermanently)
	})
	d := privateDeck(srv)
	d.ExtraHosts = []string{strings.TrimPrefix(storageURL, "http://")}
	obj, err := NewProw(nil, srv.Client()).Open(context.Background(), d, "logs/j/1/build-log.txt")
	if err != nil {
		t.Fatalf("redirect to an extra host of the deck should work: %v", err)
	}
	obj.Body.Close()
	if got := (*storageSeen)[0]; got != "" {
		t.Errorf("token forwarded to the extra host: %q", got)
	}
}

func TestProwAuth_TokenDroppedOnPlainHTTPRedirect(t *testing.T) {
	t.Setenv(testTokenEnv, secretToken)
	var hops []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops = append(hops, r.URL.Path+"|"+r.Header.Get("Authorization"))
		if r.URL.Path != "/next" {
			http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	obj, err := NewProw(nil, srv.Client()).Open(context.Background(), privateDeck(srv), "logs/j/1/build-log.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	obj.Body.Close()
	if len(hops) != 2 || !strings.HasSuffix(hops[0], "Bearer "+secretToken) {
		t.Fatalf("unexpected hops: %v", hops)
	}
	// The test server is plain http, so the redirect hop must not carry the token.
	for _, h := range hops {
		if strings.HasPrefix(h, "/next|") && h != "/next|" {
			t.Errorf("token sent on a plain http redirect hop: %q", h)
		}
	}
}

func TestProwAuth_JobHistoryAndList(t *testing.T) {
	t.Setenv(testTokenEnv, secretToken)
	srv, seen := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/job-history/"):
			io.WriteString(w, jobHistoryPage)
		default:
			io.WriteString(w, listingPage("bkt", "logs/j/1/"))
		}
	})
	c := NewProw(nil, srv.Client())
	d := privateDeck(srv)
	if _, err := c.JobHistory(context.Background(), d, "logs", "j", ""); err != nil {
		t.Fatalf("JobHistory: %v", err)
	}
	if _, err := c.List(context.Background(), d, "logs/j/1/"); err != nil {
		t.Fatalf("List: %v", err)
	}
	for i, h := range *seen {
		if h != "Bearer "+secretToken {
			t.Errorf("request %d Authorization = %q", i, h)
		}
	}
}

func TestParseDecks(t *testing.T) {
	decks, err := ParseDecks(`[{"name":"other","deck_url":"https://deck.example","gcsweb_url":"https://gcsweb.example","buckets":["b"],"token_env":"OTHER_TOKEN"}]`)
	if err != nil {
		t.Fatalf("ParseDecks: %v", err)
	}
	if len(decks) != 1 || decks[0].TokenEnv != "OTHER_TOKEN" || decks[0].Bucket() != "b" {
		t.Errorf("unexpected decks: %+v", decks)
	}
	for name, in := range map[string]string{
		"not json":  `{`,
		"no name":   `[{"deck_url":"https://d","gcsweb_url":"https://g","buckets":["b"]}]`,
		"no bucket": `[{"name":"x","deck_url":"https://d","gcsweb_url":"https://g"}]`,
		"http deck": `[{"name":"x","deck_url":"http://d","gcsweb_url":"https://g","buckets":["b"]}]`,
		"no gcsweb": `[{"name":"x","deck_url":"https://d","buckets":["b"]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDecks(in); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestMergeDecks(t *testing.T) {
	merged := MergeDecks(DefaultDecks, []Deck{
		{Name: "qe-private", DeckURL: "https://d", GCSWebURL: "https://g", Buckets: []string{"x"}},
		{Name: "new", DeckURL: "https://n", GCSWebURL: "https://ng", Buckets: []string{"y"}},
	})
	if len(merged) != len(DefaultDecks)+1 {
		t.Fatalf("got %d decks", len(merged))
	}
	if d, _ := DeckByName(merged, "qe-private"); d.Bucket() != "x" {
		t.Errorf("qe-private not replaced: %+v", d)
	}
	if d, _ := DeckByName(DefaultDecks, "qe-private"); d.Bucket() != "qe-private-deck" {
		t.Error("MergeDecks modified its input")
	}
}

func TestParseRunURL_PrivateDeck(t *testing.T) {
	ref, err := ParseRunURL(DefaultDecks, "https://qe-private-deck-ci.apps.ci.l2s4.p1.openshiftapps.com/view/gs/qe-private-deck/pr-logs/pull/openshift_release/87540/rehearse-87540-pull-ci-x/2113466804234883072")
	if err != nil {
		t.Fatalf("ParseRunURL: %v", err)
	}
	if ref.Deck.Name != "qe-private" || ref.PR != "87540" || ref.Job != "rehearse-87540-pull-ci-x" {
		t.Errorf("unexpected ref: %+v", ref)
	}
	if _, err := ParseRunURL(DefaultDecks, "https://prow.ci.openshift.org/view/gs/qe-private-deck/logs/j/1"); err == nil {
		t.Error("the private bucket must not resolve through the public deck host")
	}
}
