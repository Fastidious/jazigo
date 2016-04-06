package dev

import (
	"time"
)

func registerModelHTTP(logger hasPrintf, models map[string]*Model) {
	modelName := "http"
	m := &Model{name: modelName}

	m.defaultAttr = attributes{
		needLoginChat:               false,
		needEnabledMode:             false,
		needPagingOff:               false,
		enableCommand:               "",
		usernamePromptPattern:       "",
		passwordPromptPattern:       "",
		enablePasswordPromptPattern: "",
		disabledPromptPattern:       `\S+>\s*$`,
		enabledPromptPattern:        `\S+#\s*$`,
		commandList:                 []string{"GET / HTTP/1.0\r\n\r\n"},
		disablePagerCommand:         "",
		readTimeout:                 5 * time.Second,
		matchTimeout:                10 * time.Second,
		sendTimeout:                 5 * time.Second,
		commandReadTimeout:          5 * time.Second,  // larger timeout for slow 'sh run'
		commandMatchTimeout:         10 * time.Second, // larger timeout for slow 'sh run'
	}

	models[modelName] = m
}
