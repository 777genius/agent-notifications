//go:build windows

// This disposable CI runner removes administrator access without changing accounts or ACLs.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	if err := run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "standard-user runner:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: runner.exe <binary> <args...> | --check")
	}
	token, sid, err := restrictedToken()
	if err != nil {
		return fmt.Errorf("restrict current user: %w", err)
	}
	defer token.Close()
	if len(os.Args) == 2 && os.Args[1] == "--check" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"userSid": sid, "administrator": false, "disabledPrivileges": true,
		})
	}
	// Resolve exactly the supplied file, never a shell command or PATH substitute.
	binary, err := filepath.Abs(os.Args[1])
	if err != nil {
		return err
	}
	cmd := exec.Command(binary, os.Args[2:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(token)}
	// A nil Env with Token would load the user's defaults and discard CI fixtures.
	cmd.Env = os.Environ()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func restrictedToken() (token windows.Token, sid string, err error) {
	var original windows.Token
	access := uint32(windows.TOKEN_QUERY | windows.TOKEN_DUPLICATE | windows.TOKEN_ASSIGN_PRIMARY)
	if err = windows.OpenProcessToken(windows.CurrentProcess(), access, &original); err != nil {
		return 0, "", fmt.Errorf("open primary token: %w", err)
	}
	defer original.Close()
	user, err := original.GetTokenUser()
	if err != nil {
		return 0, "", err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return 0, "", err
	}
	disable := windows.SIDAndAttributes{Sid: admins}
	create := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")
	// DISABLE_MAX_PRIVILEGE leaves only SeChangeNotifyPrivilege. The restricted
	// version of our own primary token needs no SeAssignPrimaryToken privilege.
	ok, _, callErr := create.Call(uintptr(original), 1, 1, uintptr(unsafe.Pointer(&disable)),
		0, 0, 0, 0, uintptr(unsafe.Pointer(&token)))
	if ok == 0 {
		return 0, "", fmt.Errorf("CreateRestrictedToken: %w", callErr)
	}
	defer func() {
		if err != nil {
			token.Close()
		}
	}()
	restrictedUser, err := token.GetTokenUser()
	if err != nil {
		return token, "", err
	}
	if !restrictedUser.User.Sid.Equals(user.User.Sid) {
		return token, "", errors.New("restricted token changed the user SID")
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return token, "", err
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.Equals(admins) && (group.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY == 0 ||
			group.Attributes&windows.SE_GROUP_ENABLED != 0) {
			return token, "", errors.New("Administrators SID is not deny-only")
		}
	}
	// CheckTokenMembership requires an impersonation token, not our primary token.
	var membership windows.Token
	if err = windows.DuplicateTokenEx(token, windows.TOKEN_QUERY, nil,
		windows.SecurityIdentification, windows.TokenImpersonation, &membership); err != nil {
		return token, "", err
	}
	defer membership.Close()
	member, err := membership.IsMember(admins)
	if err != nil {
		return token, "", err
	}
	if member {
		return token, "", errors.New("restricted token still has administrator membership")
	}
	var size uint32
	if err = windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &size); err != windows.ERROR_INSUFFICIENT_BUFFER {
		return token, "", fmt.Errorf("query privilege buffer: %v", err)
	}
	buffer := make([]byte, size)
	if err = windows.GetTokenInformation(token, windows.TokenPrivileges, &buffer[0], size, &size); err != nil {
		return token, "", err
	}
	var permitted windows.LUID
	name, _ := windows.UTF16PtrFromString("SeChangeNotifyPrivilege")
	if err = windows.LookupPrivilegeValue(nil, name, &permitted); err != nil {
		return token, "", err
	}
	for _, privilege := range (*windows.Tokenprivileges)(unsafe.Pointer(&buffer[0])).AllPrivileges() {
		if privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 && privilege.Luid != permitted {
			return token, "", errors.New("restricted token retains an enabled privileged capability")
		}
	}
	return token, restrictedUser.User.Sid.String(), nil
}
