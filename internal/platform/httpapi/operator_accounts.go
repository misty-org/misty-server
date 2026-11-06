package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/email"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// The operator account API (/operator/v1) lets whoever runs this server, or
// its billing service, create accounts with limits, change the limits and
// billing period, and suspend, close (read-only, then deleted) or resume an account. It is
// authorized by MISTY_OPERATOR_TOKEN, unmounted while that is unset; docs/cloud/README.md has the contract.
// A setup key it issues comes back in the answer as setup_key and setup_link for
// the caller to pass on; a server with Mailjet configured also emails it.

// OperatorToken is MISTY_OPERATOR_TOKEN, or empty.
func OperatorToken() string { return strings.TrimSpace(config.Getenv("MISTY_OPERATOR_TOKEN")) }

// OperatorAccounts serves the operator account API.
type OperatorAccounts struct {
	database *db.Database
	guard    *SuspensionGuard
	now      func() time.Time
}

func NewOperatorAccounts(database *db.Database, guard *SuspensionGuard) *OperatorAccounts {
	return &OperatorAccounts{database: database, guard: guard, now: time.Now}
}

// operatorAccount is the account in an answer. SetupKey and SetupLink are
// there only when this request issued a key; the key is never stored, so no
// later answer can show it again.
type operatorAccount struct {
	db.OperatorAccount
	SetupKey  string `json:"setup_key,omitempty"`
	SetupLink string `json:"setup_link,omitempty"`
	// Emailed says this server is emailing the key to the account's address.
	Emailed bool `json:"emailed,omitempty"`
}

// issuedKey is a setup key this request issued.
type issuedKey struct {
	key, link string
	emailed   bool
}

// RequireOperator admits only `Authorization: Bearer <token>`. Both sides are
// hashed first, so the comparison takes the same time whatever their lengths.
func RequireOperator(token string) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			got, ok := accounts.BearerTokenFromRequest(r)
			sum := sha256.Sum256([]byte(got))
			if !ok || token == "" || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="misty-operator"`)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "operator_unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Mount registers the routes on r, which the caller has already guarded with
// RequireOperator.
func (o *OperatorAccounts) Mount(r chi.Router) {
	r.Get("/accounts", o.list)
	r.Post("/accounts", o.create)
	r.Post("/accounts/proof", o.fromProof)
	r.Get("/accounts/{id}", o.get)
	r.Patch("/accounts/{id}", o.setLimits)
	r.Post("/accounts/{id}/suspend", o.suspend)
	r.Post("/accounts/{id}/resume", o.resume)
	r.Post("/accounts/{id}/close", o.close)
	r.Post("/accounts/{id}/setup-key", o.newSetupKey)
}

// list answers ?q= (a fragment of an email or username) or ?email= (one
// exact email, case-insensitive), at most limit accounts.
func (o *OperatorAccounts) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := o.database.OperatorAccounts(r.Context(), db.OperatorAccountSearch{
		Text: r.URL.Query().Get("q"), Email: r.URL.Query().Get("email"),
	}, limit)
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": items})
}

func (o *OperatorAccounts) get(w http.ResponseWriter, r *http.Request) {
	o.writeAccount(w, r, chi.URLParam(r, "id"), http.StatusOK, nil, nil)
}

func (o *OperatorAccounts) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email  string         `json:"email"`
		Limits operatorLimits `json:"limits"`
	}
	if decodeJSON(w, r, &body) != nil {
		return
	}
	limits, err := body.Limits.apply(db.AccountLimits{})
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	user, created, err := o.database.CreateOperatorAccount(r.Context(), body.Email, limits)
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	// A retried create issues a key again only while the person has neither
	// signed in nor been given a key that still works.
	var issued *issuedKey
	state, err := o.database.AccountSetupState(r.Context(), user.ID)
	if err == nil && (created || (!state.Done && !state.LiveKey)) {
		issued, err = o.issueSetupKey(r.Context(), r, user, db.EmailSetup)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	o.writeAccount(w, r, user.ID, status, issued, map[string]any{"created": created})
}

func (o *OperatorAccounts) setLimits(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Limits       operatorLimits  `json:"limits"`
		PeriodAnchor json.RawMessage `json:"period_anchor"`
	}
	if decodeJSON(w, r, &body) != nil {
		return
	}
	id := chi.URLParam(r, "id")
	anchor, setAnchor, err := parsePeriodAnchor(body.PeriodAnchor)
	var current db.AccountLimits
	if err == nil {
		current, err = o.database.AccountLimitsFor(r.Context(), id)
	}
	if err == nil {
		current, err = body.Limits.apply(current)
	}
	if err == nil {
		err = o.database.SetAccountLimits(r.Context(), id, current)
	}
	if err == nil && setAnchor {
		err = o.database.SetAccountPeriodAnchor(r.Context(), id, anchor)
	}
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	o.writeAccount(w, r, id, http.StatusOK, nil, nil)
}

func (o *OperatorAccounts) suspend(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := o.database.SuspendAccount(r.Context(), id); err != nil {
		writeOperatorError(w, err)
		return
	}
	o.guard.Mark(id, true)
	o.writeAccount(w, r, id, http.StatusOK, nil, nil)
}

func (o *OperatorAccounts) resume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := o.database.ResumeAccount(r.Context(), id); err != nil {
		writeOperatorError(w, err)
		return
	}
	if err := o.database.CancelClosingEmails(r.Context(), id); err != nil {
		writeOperatorError(w, err)
		return
	}
	o.guard.Mark(id, false)
	o.writeAccount(w, r, id, http.StatusOK, nil, nil)
}

// fromProof answers with the account a signed-in person proved they hold
// (POST /me/account-proof), for a service that can't see their session.
func (o *OperatorAccounts) fromProof(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Proof string `json:"proof"`
	}
	if decodeJSON(w, r, &body) != nil {
		return
	}
	id, err := accountFromProof(body.Proof, o.now())
	if errors.Is(err, errAccountProofInvalid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "account_proof_invalid"})
		return
	}
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	o.writeAccount(w, r, id, http.StatusOK, nil, nil)
}

// close makes the account read-only, stops its agents and schedules its deletion
// delete_after_days from now. The account-deletion worker deletes it on the
// date. A server with Mailjet configured tells the person, with keep_url as where
// they can keep the account; otherwise telling them is the caller's job.
func (o *OperatorAccounts) close(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeleteAfterDays *int   `json:"delete_after_days"`
		KeepURL         string `json:"keep_url"`
	}
	if decodeJSON(w, r, &body) != nil {
		return
	}
	days := 30
	if body.DeleteAfterDays != nil {
		days = *body.DeleteAfterDays
	}
	if days < 1 || days > 365 {
		writeOperatorError(w, operatorInvalid("delete_after_days must be a whole number from 1 to 365"))
		return
	}
	keepURL := strings.TrimSpace(body.KeepURL)
	if parsed, err := url.Parse(keepURL); keepURL != "" && (err != nil || parsed.Scheme != "https" || parsed.Host == "") {
		writeOperatorError(w, operatorInvalid("keep_url must be an https address"))
		return
	}
	id := chi.URLParam(r, "id")
	deleteAt, err := o.database.CloseAccount(r.Context(), id, o.now().Add(time.Duration(days)*24*time.Hour))
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	o.guard.MarkClosing(id, deleteAt)
	if email.Configured() {
		user, err := o.database.GetUserByID(id)
		if err == nil && user != nil {
			err = o.database.QueueClosingEmails(r.Context(), id, user.Email, keepURL, deleteAt)
		}
		if err != nil {
			writeOperatorError(w, err)
			return
		}
	}
	o.writeAccount(w, r, id, http.StatusOK, nil, nil)
}

// newSetupKey issues a sign-in link for any password account, whether or not
// it has signed in before: for an account that has, it is how a forgotten
// password is reset (the sign-in may choose a new one).
func (o *OperatorAccounts) newSetupKey(w http.ResponseWriter, r *http.Request) {
	user, err := o.database.GetUserByID(chi.URLParam(r, "id"))
	if err == nil && (user == nil || user.Provider != "misty") {
		// A Google account signs in with Google; it has no use for a key.
		err = db.ErrAccountNotFound
	}
	var issued *issuedKey
	if err == nil {
		// An account that has signed in before gets a sign-in link, not a welcome.
		kind := db.EmailSetup
		var state db.SetupState
		if state, err = o.database.AccountSetupState(r.Context(), user.ID); err == nil {
			if state.Done {
				kind = db.EmailSignIn
			}
			issued, err = o.issueSetupKey(r.Context(), r, user, kind)
		}
	}
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	o.writeAccount(w, r, user.ID, http.StatusOK, issued, nil)
}

// issueSetupKey stores a new key (by hash) and returns it with its link.
// Earlier unused keys stop working. A server with Mailjet configured also emails
// the link, as the kind of email named.
func (o *OperatorAccounts) issueSetupKey(ctx context.Context, r *http.Request, user *db.User, kind string) (*issuedKey, error) {
	key, err := GenerateSetupKey()
	if err != nil {
		return nil, err
	}
	link := SetupLink(publicServerOrigin(r), key)
	var message *db.KeyEmail
	if email.Configured() {
		message = &db.KeyEmail{Kind: kind, Recipient: user.Email, Key: key, Link: link}
	}
	if err := o.database.IssueSetupKeyWithEmail(ctx, user.ID, security.HashToken(key), o.now().Add(db.SetupKeyTTL), message); err != nil {
		return nil, err
	}
	return &issuedKey{key: key, link: link, emailed: message != nil}, nil
}

func (o *OperatorAccounts) writeAccount(w http.ResponseWriter, r *http.Request, id string, status int, issued *issuedKey, extra map[string]any) {
	found, err := o.database.OperatorAccountByID(r.Context(), id)
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	account := operatorAccount{OperatorAccount: found}
	if issued != nil {
		account.SetupKey, account.SetupLink, account.Emailed = issued.key, issued.link, issued.emailed
	}
	body := map[string]any{"account": account}
	for key, value := range extra {
		body[key] = value
	}
	writeJSON(w, status, body)
}

// setupPageURL is the website's static setup page. It reads the server and
// key from the fragment in the browser and offers misty://setup, so the same
// page serves every server, self-hosted ones included. MISTY_WEBSITE_URL names
// another site: the local website on a development server.
func setupPageURL() string {
	return websiteURL() + "/setup"
}

func websiteURL() string {
	if site := strings.TrimRight(strings.TrimSpace(config.Getenv("MISTY_WEBSITE_URL")), "/"); site != "" {
		return site
	}
	return "https://mistysys.com"
}

// SetupLink is a setup key's sign-in link:
// https://mistysys.com/setup#server=<MISTY_HOSTNAME>&key=<key>. Without
// MISTY_HOSTNAME (a development server) the host comes from the server origin.
func SetupLink(serverOrigin, key string) string {
	return setupPageURL() + "#server=" + url.QueryEscape(serverHost(serverOrigin)) + "&key=" + url.QueryEscape(key)
}

// serverHost is the host people know this server by, for the website's pages.
func serverHost(serverOrigin string) string {
	host := strings.ToLower(strings.TrimSpace(config.Getenv("MISTY_HOSTNAME")))
	if parsed, err := url.Parse(serverOrigin); host == "" && err == nil {
		host = parsed.Host
	}
	return host
}

// publicServerOrigin is the server's public origin (MISTY_PUBLIC_API_URL
// without its /v1 path), which is the address people enter in Misty.
func publicServerOrigin(r *http.Request) string {
	if parsed, err := url.Parse(strings.TrimSpace(config.Getenv("MISTY_PUBLIC_API_URL"))); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return parsed.Scheme + "://" + parsed.Host
	}
	scheme := "http"
	if IsSecureRequest(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func writeOperatorError(w http.ResponseWriter, err error) {
	var invalid operatorInvalid
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request", "message": string(invalid)})
	case errors.Is(err, db.ErrSpaceInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request", "message": "email must be an email address"})
	case errors.Is(err, db.ErrAccountNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "account_not_found"})
	case errors.Is(err, db.ErrAccountPendingDeletion):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "account_pending_deletion", "message": "This email's account is being deleted. It can be created again once the deletion finishes."})
	default:
		log.Printf("operator account API failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
	}
}
