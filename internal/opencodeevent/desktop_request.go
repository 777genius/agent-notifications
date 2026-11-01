package opencodeevent

import (
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/google/uuid"
)

// desktopRequest is the one policy projection shared by the OpenCode consumer
// and its installed, admission-bound composition. The server event has no
// trusted local terminal target; enabling a sound never grants navigation.
func desktopRequest(content notification.Content, deadline notification.Deadline, nativeSound bool) notification.Request {
	return notification.InformationalDesktopRequest(content, uuid.NewString(), deadline, nativeSound)
}
