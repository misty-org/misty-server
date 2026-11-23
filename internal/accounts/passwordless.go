package accounts

func (db Store) CreatePasswordlessUser(username, email string) (*User, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return nil, err
	}
	return db.createIdentity(normalized, email, "", "misty", "", true)
}
