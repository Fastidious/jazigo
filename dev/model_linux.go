package dev

import (
	"time"
)

func registerModelLinux(logger hasPrintf, t *DeviceTable) {
	modelName := "linux"
	m := &Model{name: modelName}

	m.defaultAttr = attributes{
		needLoginChat:               true,
		needEnabledMode:             false,
		needPagingOff:               false,
		enableCommand:               "",
		usernamePromptPattern:       `Username:\s*$`,
		passwordPromptPattern:       `Password:\s*$`,
		enablePasswordPromptPattern: "",
		disabledPromptPattern:       `\$\s*$`,
		enabledPromptPattern:        `\$\s*$`,
		commandList:                 []string{"", "/bin/uname -a", "/usr/bin/uptime", "/bin/ls"},
		disablePagerCommand:         "",
		readTimeout:                 5 * time.Second,
		matchTimeout:                10 * time.Second,
		sendTimeout:                 5 * time.Second,
		commandReadTimeout:          10 * time.Second,
		commandMatchTimeout:         10 * time.Second,
	}

	if err := t.SetModel(m, logger); err != nil {
		logger.Printf("registerModelLinux: %v", err)
	}
}
