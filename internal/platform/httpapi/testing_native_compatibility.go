package api

import (
	mailintegration "github.com/kannachi323/misty/server/internal/integrations/mail"
)

func TestingMailThreadToDTO(thread mailintegration.Thread) mailThreadDTO {
	return mailThreadToDTO(thread)
}
