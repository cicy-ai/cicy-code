package main

import (
	"sort"
	"strings"
)

func runtimeAIProviderNames() []string {
	cfg := readGlobalJSONConfig()
	ai := cfgMapValue(cfg, "ai")
	providerMap := cfgMapValue(ai, "provider")
	if len(providerMap) == 0 {
		return nil
	}
	names := make([]string, 0, len(providerMap))
	for key := range providerMap {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
