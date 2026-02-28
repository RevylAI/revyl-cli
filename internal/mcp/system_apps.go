package mcp

import (
	"sort"
	"strings"
)

// systemApps maps platform -> friendly_name -> bundle_id.
// Source of truth: cognisim_schemas/schemas/system_apps.py
var systemApps = map[string]map[string]string{
	"ios": {
		"settings":  "com.apple.Preferences",
		"safari":    "com.apple.mobilesafari",
		"maps":      "com.apple.Maps",
		"photos":    "com.apple.mobileslideshow",
		"contacts":  "com.apple.MobileAddressBook",
		"files":     "com.apple.DocumentsApp",
		"calendar":  "com.apple.mobilecal",
		"messages":  "com.apple.MobileSMS",
		"reminders": "com.apple.reminders",
	},
	"android": {
		"settings": "com.android.settings",
		"chrome":   "com.android.chrome",
		"phone":    "com.google.android.dialer",
		"contacts": "com.google.android.contacts",
		"messages": "com.google.android.apps.messaging",
		"camera":   "com.android.camera2",
		"photos":   "com.google.android.apps.photos",
		"files":    "com.google.android.documentsui",
		"calendar": "com.google.android.calendar",
		"clock":    "com.google.android.deskclock",
		"maps":     "com.google.android.apps.maps",
		"gmail":    "com.google.android.gm",
		"youtube":  "com.google.android.youtube",
	},
}

// ResolveSystemApp resolves a friendly app name to a bundle ID for the given platform.
// Returns the input unchanged if no match (allows raw bundle IDs as fallback).
func ResolveSystemApp(platform, appName string) string {
	appName = strings.ToLower(appName)
	if apps, ok := systemApps[platform]; ok {
		if bundleID, ok := apps[appName]; ok {
			return bundleID
		}
	}
	return appName
}

// ListSystemApps returns available system app names for a platform (sorted).
func ListSystemApps(platform string) []string {
	apps, ok := systemApps[platform]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(apps))
	for name := range apps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SystemAppDisplayName returns the friendly name for a bundle ID, or empty string if not found.
func SystemAppDisplayName(platform, bundleID string) string {
	if apps, ok := systemApps[platform]; ok {
		for name, bid := range apps {
			if bid == bundleID {
				return name
			}
		}
	}
	return ""
}
