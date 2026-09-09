package api

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSDKBackendCredentialBindsUserAppConnectionAndRevision(t *testing.T) {
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	service := &SpacesService{aead: aead, keyVer: 1}
	encrypted, err := service.encryptSDKBackendBearer("owner", "example.habits", "connection", 2, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("private-token")) {
		t.Fatal("credential stored in plaintext")
	}
	connection := db.SDKBackendConnection{UserID: "owner", AppID: "example.habits", ID: "connection", Revision: 2, KeyVersion: 1, BearerCiphertext: encrypted}
	token, err := service.decryptSDKBackendBearer(connection)
	if err != nil || token != "private-token" {
		t.Fatalf("decrypt: %v", err)
	}
	for name, change := range map[string]func(*db.SDKBackendConnection){
		"user":        func(c *db.SDKBackendConnection) { c.UserID = "other" },
		"app":         func(c *db.SDKBackendConnection) { c.AppID = "another.app" },
		"connection":  func(c *db.SDKBackendConnection) { c.ID = "other" },
		"revision":    func(c *db.SDKBackendConnection) { c.Revision = 3 },
		"key version": func(c *db.SDKBackendConnection) { c.KeyVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := connection
			change(&changed)
			if _, err := service.decryptSDKBackendBearer(changed); err == nil {
				t.Fatal("credential binding bypassed")
			}
		})
	}
}
