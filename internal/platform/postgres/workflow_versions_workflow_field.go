package db

import (
	"time"
)






type SpaceIntegration struct {
	ID                  string    `json:"id"`
	SpaceID             string    `json:"space_id"`
	Provider            string    `json:"provider"`
	DisplayName         string    `json:"display_name"`
	CredentialReference string    `json:"-"`
	GrantedPermissions  []string  `json:"granted_permissions"`
	Status              string    `json:"status"`
	ConnectedByUserID   string    `json:"connected_by_user_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}


