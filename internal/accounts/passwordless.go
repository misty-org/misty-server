package accounts

// CreatePasswordlessUser makes a Misty account with no password. The operator
// account API uses it: the person signs in once with a setup key (a sign-in link) and
// sets a password afterwards (ChangePassword accepts an empty current
// password for exactly this case). Password sign-in fails until then, because
// no bcrypt hash matches the empty one.
func (db Store) CreatePasswordlessUser(username, email string) (*User, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return nil, err
	}
	return db.createIdentity(normalized, email, "", "misty", "")
}
