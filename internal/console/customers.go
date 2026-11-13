package console

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// The Customers page lists the server's accounts with what each uses against
// its limits. Reads come from the server's own account queries; every change
// goes through the operator account API (operatorClient).

type customersView struct {
	Search    string
	Customers []db.OperatorAccount
	Selected  string
	Detail    *customerDetailView
	Err       string
}

type customerDetailView struct {
	Account   db.OperatorAccount
	Console   db.ConsoleAccountDetail
	CanChange bool
	Flash     string
	Err       string
	// SignInLink is a link just issued, shown once to copy and pass on.
	SignInLink string
}

func (v customerDetailView) actionPath(action string) string {
	return "/customers/" + v.Account.ID + "/" + action
}

// customerStatus is the one state label a row shows.
func customerStatus(a db.OperatorAccount) string {
	switch {
	case a.DeleteAt != nil:
		return "closing"
	case a.Suspended:
		return "suspended"
	case a.Owner:
		return "owner"
	case !a.Setup.Done:
		return "awaiting_setup"
	}
	return "active"
}

func (s *Server) customersPage(r *http.Request) templ.Component {
	if s.db == nil {
		return databaseUnavailable("Customers")
	}
	return customersPage(s.customersViewFor(r, r.URL.Query().Get("id")))
}

// customersViewFor loads the list and the selected account, or the first one
// when none is selected.
func (s *Server) customersViewFor(r *http.Request, selected string) customersView {
	view := customersView{Search: strings.TrimSpace(r.URL.Query().Get("q")), Selected: selected}
	customers, err := s.db.OperatorAccounts(r.Context(), db.OperatorAccountSearch{Text: view.Search}, 100)
	if err != nil {
		view.Err = "Couldn't load customers: " + err.Error()
		return view
	}
	view.Customers = customers
	if view.Selected == "" && len(customers) > 0 {
		view.Selected = customers[0].ID
	}
	if view.Selected != "" {
		detail := s.loadCustomer(r.Context(), view.Selected)
		view.Detail = &detail
	}
	return view
}

func (s *Server) loadCustomer(ctx context.Context, userID string) customerDetailView {
	view := customerDetailView{CanChange: s.operator.configured()}
	account, err := s.db.OperatorAccountByID(ctx, userID)
	if errors.Is(err, db.ErrAccountNotFound) {
		view.Err = "That account no longer exists."
		return view
	} else if err != nil {
		view.Err = "Couldn't load the account: " + err.Error()
		return view
	}
	view.Account = account
	if detail, err := s.db.ConsoleAccountDetail(ctx, userID); err == nil {
		view.Console = detail
	} else if !errors.Is(err, sql.ErrNoRows) {
		view.Err = "Couldn't load the account's sessions and storage: " + err.Error()
	}
	return view
}

// renderCustomerAction re-renders the detail panel after an action with a
// confirmation or the error that stopped it.
func (s *Server) renderCustomerAction(w http.ResponseWriter, r *http.Request, flash string, actionErr error) {
	view := s.loadCustomer(r.Context(), chi.URLParam(r, "id"))
	if actionErr != nil {
		view.Err = actionErr.Error()
	} else {
		view.Flash = flash
	}
	render(w, r, customerDetail(view))
}

// signInLink issues a new setup key and shows its link once. The operator
// passes the link on, to onboard someone or to reset a forgotten password (the
// sign-in lets them choose a new one). A server with Mailjet configured also
// emails it.
func (s *Server) signInLink(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.NotFound(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	account, err := s.db.OperatorAccountByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	link, err := s.operator.signInLink(r.Context(), id)
	if err != nil {
		s.renderCustomerAction(w, r, "", err)
		return
	}
	s.audit(r, "Made a sign-in link for", account.Email, "")
	view := s.loadCustomer(r.Context(), id)
	view.Flash = "New sign-in link; earlier unused links stop working"
	view.SignInLink = link
	render(w, r, customerDetail(view))
}

// customerAction runs one operator API call for the account in the URL and
// records it in the audit log when it worked.
func (s *Server) customerAction(audit, flash string, call func(context.Context, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.db == nil {
			http.NotFound(w, r)
			return
		}
		id := chi.URLParam(r, "id")
		account, err := s.db.OperatorAccountByID(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := call(r.Context(), id); err != nil {
			s.renderCustomerAction(w, r, "", err)
			return
		}
		s.audit(r, audit, account.Email, "")
		s.renderCustomerAction(w, r, flash, nil)
	}
}

func (s *Server) setCustomerLimits(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderCustomerAction(w, r, "", errors.New("Couldn't read the form."))
		return
	}
	limits, err := limitsFromForm(r.PostForm)
	if err != nil {
		s.renderCustomerAction(w, r, "", err)
		return
	}
	s.customerAction("Set limits for", "Limits saved", func(ctx context.Context, id string) error {
		return s.operator.setLimits(ctx, id, limits)
	})(w, r)
}

func (s *Server) revokeSessions(w http.ResponseWriter, r *http.Request) {
	s.customerAction("Revoked sessions for", "Signed out everywhere", func(ctx context.Context, id string) error {
		_, err := s.db.ConsoleRevokeSessions(ctx, id)
		return err
	})(w, r)
}
