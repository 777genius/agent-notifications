// Package uapinstaller is the public installer API described in
// docs/plans/uap-installer-sdk-and-notification-wizard-plan.md §2.4 and §5.
//
// The long-term home is UAP install/integrationctl/agentplugins/installer.
// This module path is a Notifications-hosted beta until that package can be
// published from the UAP repository. Callers must not import raw Store/Kernel
// types through this package; composition stays inside New.
// Inspect reports pending journals and unfinished state receipts without
// recovering them. Recover takes that observation and refuses a changed scope.
// Request.Targets selects Claude and Codex in one operation; Switch remains unpublished.
package uapinstaller
