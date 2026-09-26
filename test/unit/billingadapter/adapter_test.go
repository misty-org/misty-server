package billingadapter

import (
	"context"
	"errors"
	. "github.com/kannachi323/misty/server/internal/billingadapter"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func request() Request {
	return Request{Version: 1, AccountID: "account", Operation: "agent", OperationID: "run", Key: "turn:1", Usage: Usage{Units: map[string]int64{"input_tokens": 12}}}
}
func TestDisabledRequiresNoService(t *testing.T) {
	a, err := New(Config{})
	if err != nil || a.Enabled() {
		t.Fatal(a, err)
	}
	d, err := a.Do(context.Background(), "reserve", request())
	if err != nil || !d.Allowed {
		t.Fatal(d, err)
	}
}
func TestConfigurationFailsClosed(t *testing.T) {
	for _, c := range []Config{{Hosted: true}, {Mode: "none", Hosted: true}, {Mode: "htp"}, {Mode: "http", URL: "http://example.com", Secret: "12345678901234567890123456789012"}, {Mode: "http", URL: "https://billing.example", Secret: "short"}} {
		if _, err := New(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}
func TestSignedAdmissionAndFailures(t *testing.T) {
	const secret = "12345678901234567890123456789012"
	status := 200
	body := `{"allowed":true,"reservation_id":"opaque"}`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/adapter/v1/reserve" || r.Header.Get("X-Misty-Billing-Signature") != Signature([]byte(secret), r.Header.Get("X-Misty-Billing-Timestamp"), r.Method, r.URL.Path, raw) {
			t.Error("unsigned or incorrectly targeted request")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	a, err := New(Config{Mode: "http", URL: server.URL + "/adapter", Secret: secret, AllowLoopbackHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.Do(context.Background(), "reserve", request())
	if err != nil || d.ReservationID != "opaque" {
		t.Fatal(d, err)
	}
	for _, test := range []struct {
		status int
		body   string
		want   error
	}{{402, `{}`, ErrDenied}, {503, `{}`, ErrUnavailable}, {200, `not-json`, ErrUnavailable}, {200, `{"allowed":true}`, ErrUnavailable}, {200, `{"allowed":false}`, ErrDenied}, {302, `{}`, ErrUnavailable}} {
		status, body = test.status, test.body
		if _, err = a.Do(context.Background(), "reserve", request()); !errors.Is(err, test.want) {
			t.Fatalf("%d %s: %v", status, body, err)
		}
	}
	before := calls
	r := request()
	r.Usage.Units["input_tokens"] = -1
	if _, err = a.Do(context.Background(), "reserve", r); !errors.Is(err, ErrInvalid) || calls != before {
		t.Fatal("invalid usage reached service")
	}
}

type fakeStore struct {
	entries map[string]Entry
	fail    bool
}

func (s *fakeStore) Enqueue(_ context.Context, e Entry) error {
	if s.fail {
		return errors.New("storage failure")
	}
	s.entries[e.ID] = e
	return nil
}
func (s *fakeStore) Pending(context.Context, int) ([]Entry, error) {
	var out []Entry
	for _, e := range s.entries {
		out = append(out, e)
	}
	return out, nil
}
func (s *fakeStore) Delivered(_ context.Context, id string) error   { delete(s.entries, id); return nil }
func (s *fakeStore) Retry(context.Context, string, time.Time) error { return nil }

type fakeAdapter struct {
	fail bool
	keys []string
}

func (*fakeAdapter) Enabled() bool { return true }
func (a *fakeAdapter) Do(_ context.Context, _ string, r Request) (Decision, error) {
	a.keys = append(a.keys, r.Key)
	if a.fail {
		return Decision{}, ErrUnavailable
	}
	return Decision{Allowed: true}, nil
}
func TestSettlementSurvivesOutageAndRetriesSameKey(t *testing.T) {
	a := &fakeAdapter{fail: true}
	s := &fakeStore{entries: map[string]Entry{}}
	r := Reliable{Adapter: a, Store: s}
	req := request()
	req.ReservationID = "reservation"
	if err := r.Submit(context.Background(), "settle", req); err != nil {
		t.Fatal(err)
	}
	if len(s.entries) != 1 {
		t.Fatal("usage was not durably accepted")
	}
	a.fail = false
	if err := r.Flush(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if len(s.entries) != 0 || len(a.keys) != 2 || a.keys[0] != a.keys[1] {
		t.Fatal("retry was not idempotent", a.keys)
	}
	s.fail = true
	if err := r.Submit(context.Background(), "settle", req); err == nil {
		t.Fatal("lost usage was reported accepted")
	}
}

type memoryDurable struct {
	fakeStore
	reservations map[string]Reservation
}

func (s *memoryDurable) Reservation(_ context.Context, r Request) (*Reservation, error) {
	stored, ok := s.reservations[r.AccountID+":"+r.Key]
	if !ok {
		return nil, nil
	}
	return &stored, nil
}
func (s *memoryDurable) SaveReservation(_ context.Context, r Reservation) error {
	s.reservations[r.Admission.AccountID+":"+r.Admission.Key] = r
	return nil
}
func TestPersistedAdmissionCanCompleteDuringServiceOutage(t *testing.T) {
	a := &fakeAdapter{}
	store := &memoryDurable{fakeStore: fakeStore{entries: map[string]Entry{}}, reservations: map[string]Reservation{}}
	service := Service{Adapter: a, Store: store}
	req := request()
	// Persist the result of an earlier successful admission, as a restarted worker
	// would load it. Completion must not depend on an online admission endpoint.
	if err := store.SaveReservation(context.Background(), Reservation{ID: "opaque", Admission: req}); err != nil {
		t.Fatal(err)
	}
	a.fail = true
	reservation, err := service.Reserve(context.Background(), req)
	if err != nil || len(a.keys) != 0 {
		t.Fatal(reservation, err, a.keys)
	}
	if err = service.Complete(context.Background(), "settle", reservation, "settle:1", Usage{Units: map[string]int64{"input_tokens": 10}}, ""); err != nil {
		t.Fatal(err)
	}
	if len(store.entries) != 1 {
		t.Fatal("completion was lost")
	}
}

func (s *memoryDurable) BeginAdmission(context.Context, Request) error               { return nil }
func (s *memoryDurable) AbandonedAdmissions(context.Context, int) ([]Request, error) { return nil, nil }
func (s *memoryDurable) AdmissionRecovered(context.Context, Request) error           { return nil }
