package cursorinstall

// SupportsLinuxSourceKernel bounds the Linux source qualification to exact
// observed kernels. It grants no filesystem authority: callers must still verify
// Ubuntu 24.04.5, ext4 ancestry and the original profile token. Native installed
// qualification and user consent remain separate requirements.
func SupportsLinuxSourceKernel(kernel string) bool {
	switch kernel {
	case "6.17.0-1022-azure", "7.0.0-1017-azure":
		return true
	default:
		return false
	}
}
