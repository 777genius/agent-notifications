//go:build darwin || linux

package installruntime

// Linux too: btrfs gives each subvolume an anonymous st_dev that can change
// across boots, so a persisted dev:ino goes stale on an untouched directory (#585).
const persistedDevRelaxed = true
