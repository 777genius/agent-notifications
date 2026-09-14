// Package uapinstaller is the P1 public installer API described in
// docs/plans/uap-installer-sdk-and-notification-wizard-plan.md §2.4 and §5.
//
// The long-term home is UAP install/integrationctl/agentplugins/installer.
// This module path is a Notifications-hosted beta until that package can be
// published from the UAP repository. Callers must not import raw Store/Kernel
// types through this package; composition stays inside New.
package uapinstaller
