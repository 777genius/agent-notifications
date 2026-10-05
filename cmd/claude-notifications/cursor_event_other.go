//go:build !linux

package main

import "github.com/777genius/agent-notifications/internal/notification"

func newCursorDesktopPort() notification.DeliveryPort { return nil }
