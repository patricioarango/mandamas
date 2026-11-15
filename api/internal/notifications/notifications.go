// Package notifications sends FCM pushes to the app's registered devices.
package notifications

import (
	"context"
	"log/slog"
	"strings"

	"firebase.google.com/go/v4/messaging"

	"github.com/mandamas/api/internal/store"
)

// Event types, sent as data.type so the app can open the right screen.
const (
	TypeQuestion = "question"
	TypeReminder = "reminder"
	TypePROpen   = "pr_open"
	TypeFailed   = "failed"
)

const maxBody = 120

type Sender interface {
	SendEachForMulticast(ctx context.Context, m *messaging.MulticastMessage) (*messaging.BatchResponse, error)
}

type Devices interface {
	ListDevices(ctx context.Context) ([]store.Device, error)
	DeleteDevice(ctx context.Context, id string) error
}

type Notifier struct {
	send    Sender
	devices Devices
}

func New(s Sender, d Devices) *Notifier { return &Notifier{send: s, devices: d} }

// Notify pushes {title, body} with data {taskId, type} to every device. Failures are logged, never returned:
// a missed push must not break the task flow. Never put secrets or code in title/body.
func (n *Notifier) Notify(ctx context.Context, taskID, kind, title, body string) {
	if n == nil || n.send == nil {
		return
	}
	devs, err := n.devices.ListDevices(ctx)
	if err != nil {
		slog.Error("push: list devices", "err", err)
		return
	}
	if len(devs) == 0 {
		slog.Info("push: no devices registered", "task", taskID, "type", kind)
		return
	}
	for start := 0; start < len(devs); start += 500 {
		chunk := devs[start:min(start+500, len(devs))]
		tokens := make([]string, len(chunk))
		for i, d := range chunk {
			tokens[i] = d.Token
		}
		res, err := n.send.SendEachForMulticast(ctx, &messaging.MulticastMessage{
			Tokens:       tokens,
			Notification: &messaging.Notification{Title: Truncate(title, 80), Body: Truncate(body, maxBody)},
			Data:         map[string]string{"taskId": taskID, "type": kind},
		})
		if err != nil {
			slog.Error("push: send", "task", taskID, "type", kind, "err", err)
			continue
		}
		for i, r := range res.Responses {
			if r.Success || r.Error == nil {
				continue
			}
			if staleToken(r.Error) {
				if err := n.devices.DeleteDevice(ctx, chunk[i].ID); err != nil {
					slog.Error("push: delete stale device", "device", chunk[i].ID, "err", err)
				} else {
					slog.Info("push: removed stale device", "device", chunk[i].ID)
				}
				continue
			}
			slog.Error("push: device failed", "device", chunk[i].ID, "err", r.Error)
		}
		slog.Info("push sent", "task", taskID, "type", kind, "ok", res.SuccessCount, "failed", res.FailureCount)
	}
}

// staleToken: unregistered, or INVALID_ARGUMENT about the token itself (not the payload, which would hit every device).
func staleToken(err error) bool {
	return messaging.IsRegistrationTokenNotRegistered(err) ||
		(messaging.IsInvalidArgument(err) && strings.Contains(strings.ToLower(err.Error()), "registration token"))
}

// Truncate shortens s to n characters, adding an ellipsis.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
