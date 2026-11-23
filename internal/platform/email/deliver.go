package email

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	deliverInterval = 30 * time.Second
	deliverBatch    = 20
)

func Run(ctx context.Context, database *db.Database) {
	client, err := FromEnv()
	if err != nil {
		log.Printf("email is off: %v", err)
		return
	}
	if client == nil {
		return
	}
	ticker := time.NewTicker(deliverInterval)
	defer ticker.Stop()
	for {
		if err := DeliverDue(ctx, database, client); err != nil && ctx.Err() == nil {
			log.Printf("email delivery: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func DeliverDue(ctx context.Context, database *db.Database, sender Sender) error {
	due, err := database.ClaimDueEmails(ctx, deliverBatch)
	if err != nil {
		return err
	}
	for _, row := range due {
		message, ok := messageFor(row)
		if !ok {
			if err := database.CancelUnusableEmail(ctx, row.ID); err != nil {
				return err
			}
			continue
		}
		if sendErr := sender.Send(ctx, message); sendErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("email %s (%s) not sent: %s", row.ID, row.Kind, sendErrorCode(sendErr))
			if err := database.MarkEmailFailed(ctx, row.ID, sendErrorCode(sendErr)); err != nil {
				return err
			}
			continue
		}
		if err := database.MarkEmailSent(ctx, row.ID); err != nil {
			return err
		}
	}
	return nil
}

func sendErrorCode(err error) string {
	var refusal *Error
	if errors.As(err, &refusal) {
		code := "mailjet_" + strconv.Itoa(refusal.Status)
		if refusal.Code != "" && len(refusal.Code) <= 40 {
			code += "_" + refusal.Code
		}
		return code
	}
	return "send_failed"
}
