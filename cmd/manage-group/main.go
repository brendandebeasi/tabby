package main

import (
	"fmt"
	"os"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: manage-group [add|delete|rename|set-color|set-marker] <args>")
		os.Exit(1)
	}

	action := os.Args[1]
	configPath := config.DefaultConfigPath()

	switch action {
	case "add":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: manage-group add <name>")
			os.Exit(1)
		}
		name := os.Args[2]
		if err := addGroup(configPath, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Added group: %s\n", name)

	case "delete":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: manage-group delete <name>")
			os.Exit(1)
		}
		name := os.Args[2]
		if err := deleteGroup(configPath, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Deleted group: %s\n", name)

	case "rename":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: manage-group rename <old-name> <new-name>")
			os.Exit(1)
		}
		oldName := os.Args[2]
		newName := os.Args[3]
		if err := renameGroup(configPath, oldName, newName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Renamed group: %s -> %s\n", oldName, newName)

	case "set-color":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: manage-group set-color <name> <color>")
			os.Exit(1)
		}
		name := os.Args[2]
		color := os.Args[3]
		if err := setGroupColor(configPath, name, color); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Set color for group %s: %s\n", name, color)

	case "set-marker":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: manage-group set-marker <name> <marker>")
			os.Exit(1)
		}
		name := os.Args[2]
		marker := os.Args[3]
		if err := setGroupMarker(configPath, name, marker); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Set marker for group %s: %s\n", name, marker)

	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", action)
		os.Exit(1)
	}
}

func addGroup(configPath, name string) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	newGroup := config.DefaultGroup(name)
	if err := config.AddGroup(cfg, newGroup); err != nil {
		return err
	}

	return config.SaveConfig(configPath, cfg)
}

func deleteGroup(configPath, name string) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	if err := config.DeleteGroup(cfg, name); err != nil {
		return err
	}

	return config.SaveConfig(configPath, cfg)
}

func renameGroup(configPath, oldName, newName string) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	group := config.FindGroup(cfg, oldName)
	if group == nil {
		return config.ErrGroupNotFound
	}

	// Check if new name already exists
	if existing := config.FindGroup(cfg, newName); existing != nil {
		return config.ErrGroupExists
	}

	// Update the group name and pattern
	group.Name = newName
	group.Pattern = fmt.Sprintf("^%s\\|", newName)

	return config.SaveConfig(configPath, cfg)
}

func setGroupColor(configPath, name, color string) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	group := config.FindGroup(cfg, name)
	if group == nil {
		return config.ErrGroupNotFound
	}

	// Update colors - use the provided color as base
	group.Theme.Bg = color
	group.Theme.ActiveBg = darkenColor(color)

	return config.SaveConfig(configPath, cfg)
}

func setGroupMarker(configPath, name, marker string) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	group := config.FindGroup(cfg, name)
	if group == nil {
		return config.ErrGroupNotFound
	}

	group.Theme.Icon = marker
	return config.SaveConfig(configPath, cfg)
}

// darkenColor returns a slightly darker version of the color for active state
func darkenColor(hex string) string {
	// Simple approach: just return the color as-is for now
	// A proper implementation would parse and darken the color
	return hex
}
