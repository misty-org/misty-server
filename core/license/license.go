package license

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kannachi323/misty/server/db"
)

type Claims struct {
	UserID string  `json:"user_id"`
	Email  string  `json:"email"`
	Tier   db.Tier `json:"tier"`
	jwt.RegisteredClaims
}

func getSecret() []byte {
	return []byte(os.Getenv("LICENSE_SECRET"))
}

func Issue(user *db.User, sub *db.Subscription) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: user.ID,
		Email:  user.Email,
		Tier:   sub.Tier,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(getSecret())
}

func Validate(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return getSecret(), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid license token")
	}

	return claims, nil
}
