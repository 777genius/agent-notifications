//go:build !darwin && !linux && !windows

package main

import "github.com/777genius/agent-notifications/internal/notification"

func newGeminiDesktopPort(geminiGate) notification.DeliveryPort { return nil }
