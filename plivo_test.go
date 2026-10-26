package plivo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// rewriteTransport sends every request to the test server while keeping the
// original path so the URL construction can be asserted.
type rewriteTransport struct {
	target *url.URL
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{
		AuthID:     "MAXXXXXXXXXXXXXXXXXX",
		AuthToken:  "secret",
		HTTPClient: &http.Client{Transport: rewriteTransport{target: u}},
	}
}

func TestMessage(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if want := "/v1/Account/MAXXXXXXXXXXXXXXXXXX/Message/"; r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "MAXXXXXXXXXXXXXXXXXX" || pass != "secret" {
			t.Errorf("basic auth = %q/%q/%v", user, pass, ok)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["src"] != "15550001111" || body["dst"] != "15550002222" || body["text"] != "hi" {
			t.Errorf("unexpected body %v", body)
		}
		// Optional fields must be omitted when empty.
		for _, k := range []string{"type", "url", "method", "log"} {
			if _, ok := body[k]; ok {
				t.Errorf("optional field %q should be omitted", k)
			}
		}

		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"api_id":"abc","message":"message(s) queued","message_uuid":["u1"]}`))
	})

	res, err := c.Message(&Message{Src: "15550001111", Dst: "15550002222", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ApiID != "abc" || res.Msg != "message(s) queued" || len(res.MsgUUID) != 1 || res.MsgUUID[0] != "u1" {
		t.Fatalf("unexpected response %+v", res)
	}
}

func TestCall(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if want := "/v1/Account/MAXXXXXXXXXXXXXXXXXX/Call/"; r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		var call Call
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			t.Fatal(err)
		}
		if call.From != "1" || call.To != "2" || call.AnswerURL != "https://example.com/answer" || call.TimeLimit != 30 {
			t.Errorf("unexpected call %+v", call)
		}
		w.Write([]byte(`{"api_id":"id","message":"call fired","request_uuid":"req"}`))
	})

	res, err := c.Call(&Call{From: "1", To: "2", AnswerURL: "https://example.com/answer", TimeLimit: 30})
	if err != nil {
		t.Fatal(err)
	}
	if res.ReqUUID != "req" || res.Msg != "call fired" {
		t.Fatalf("unexpected response %+v", res)
	}
}

func TestAPIErrorIsDecoded(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"api_id":"id","error":"invalid dst"}`))
	})

	res, err := c.Message(&Message{Src: "1", Dst: "x", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "invalid dst" {
		t.Fatalf("Error = %q, want %q", res.Error, "invalid dst")
	}
}

func TestConcurrentUseWithDefaultClient(t *testing.T) {
	// A zero HTTPClient must not be written to during requests, otherwise
	// concurrent use of the Client is a data race (caught by -race).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := &Client{AuthID: "id", AuthToken: "tok"}
	done := make(chan struct{})
	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			// Requests go to the real BaseURL host; use send directly so the
			// test stays local.
			c.send(srv.URL, http.MethodPost, &Message{})
		}()
	}
	for range 4 {
		<-done
	}
	if c.HTTPClient != nil {
		t.Fatal("send must not modify Client.HTTPClient")
	}
}
