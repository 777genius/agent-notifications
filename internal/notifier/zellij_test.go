package notifier

import (
	"os"
	"strings"
	"testing"
)

func TestIsZellij(t *testing.T) {
	oldVal := os.Getenv("ZELLIJ")
	t.Cleanup(func() {
		if oldVal != "" {
			os.Setenv("ZELLIJ", oldVal)
		} else {
			os.Unsetenv("ZELLIJ")
		}
	})

	os.Setenv("ZELLIJ", "0")
	if !IsZellij() {
		t.Error("IsZellij() should return true when ZELLIJ env is set")
	}

	os.Unsetenv("ZELLIJ")
	if IsZellij() {
		t.Error("IsZellij() should return false when ZELLIJ env is not set")
	}
}

func TestParseActiveTabName(t *testing.T) {
	tests := []struct {
		name     string
		layout   string
		expected string
	}{
		{
			name: "multi_tab_focused_in_middle",
			layout: `layout {
    tab name="default" {
        pane borderless=true
    }
    tab name="editor" focus=true {
        pane borderless=true
    }
    tab name="logs" {
        pane borderless=true
    }
}`,
			expected: "editor",
		},
		{
			name: "focus_before_name",
			layout: `layout {
    tab focus=true name="build" {
        pane borderless=true
    }
    tab name="run" {
        pane borderless=true
    }
}`,
			expected: "build",
		},
		{
			name: "default_tab_name",
			layout: `layout {
    tab name="Tab #1" focus=true {
        pane borderless=true
    }
}`,
			expected: "Tab #1",
		},
		{
			name: "nested_panes_not_confused",
			layout: `layout {
    tab name="main" focus=true {
        pane split_direction="vertical" {
            pane size="50%"
            pane size="50%"
        }
    }
    tab name="other" {
        pane borderless=true
    }
}`,
			expected: "main",
		},
		{
			name:     "empty_layout",
			layout:   "",
			expected: "",
		},
		{
			name: "no_focus_true",
			layout: `layout {
    tab name="tab1" {
        pane borderless=true
    }
    tab name="tab2" {
        pane borderless=true
    }
}`,
			expected: "",
		},
		{
			name: "first_tab_focused",
			layout: `layout {
    tab name="first" focus=true {
        pane borderless=true
    }
    tab name="second" {
        pane borderless=true
    }
    tab name="third" {
        pane borderless=true
    }
}`,
			expected: "first",
		},
		{
			name: "last_tab_focused",
			layout: `layout {
    tab name="alpha" {
        pane borderless=true
    }
    tab name="beta" {
        pane borderless=true
    }
    tab name="gamma" focus=true {
        pane borderless=true
    }
}`,
			expected: "gamma",
		},
		{
			name: "tab_with_extra_attributes",
			layout: `layout {
    tab name="dev" hide_floating_panes=true focus=true cwd="/home/user" {
        pane borderless=true
    }
}`,
			expected: "dev",
		},
		{
			name: "tab_name_with_spaces",
			layout: `layout {
    tab name="my project" focus=true {
        pane borderless=true
    }
}`,
			expected: "my project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseActiveTabName(tt.layout)
			if got != tt.expected {
				t.Errorf("parseActiveTabName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBuildZellijNotifierArgs(t *testing.T) {
	args := buildZellijNotifierArgs("Title", "Message", "editor", "my-session", "com.test.app")

	if !containsArg(args, "-title", "Title") {
		t.Error("Missing -title")
	}
	if !containsArg(args, "-message", "Message") {
		t.Error("Missing -message")
	}
	if !containsArg(args, "-activate", "com.test.app") {
		t.Error("Missing -activate")
	}

	executeCmd := getArgValue(args, "-execute")
	if executeCmd == "" {
		t.Fatal("Missing -execute argument")
	}
}

func TestBuildZellijNotifierArgs_ExecuteContainsSessionAndTab(t *testing.T) {
	args := buildZellijNotifierArgs("Title", "Message", "my-tab", "test-session", "com.test.app")

	executeCmd := getArgValue(args, "-execute")
	if executeCmd == "" {
		t.Fatal("Missing -execute argument")
	}

	if !strings.Contains(executeCmd, "test-session") {
		t.Errorf("-execute should contain session name 'test-session', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "my-tab") {
		t.Errorf("-execute should contain tab name 'my-tab', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "go-to-tab-name") {
		t.Errorf("-execute should contain 'go-to-tab-name', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "-s") {
		t.Errorf("-execute should contain '-s' for session, got: %s", executeCmd)
	}
}

func TestExtractKDLStringAttr(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		key      string
		expected string
	}{
		{"basic", `tab name="editor" focus=true`, "name", "editor"},
		{"missing_key", `tab focus=true`, "name", ""},
		{"empty_value", `tab name="" focus=true`, "name", ""},
		{"value_with_spaces", `tab name="my tab" focus=true`, "name", "my tab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractKDLStringAttr(tt.line, tt.key)
			if got != tt.expected {
				t.Errorf("extractKDLStringAttr(%q, %q) = %q, want %q", tt.line, tt.key, got, tt.expected)
			}
		})
	}
}
