// Package managegroup edits window-group entries in the tabby config file.
// Exported as the `tabby manage-group` subcommand.
package managegroup

import (
	"fmt"
	"os"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func Run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tabby manage-group [add|delete|rename|set-color|set-marker] <args>")
		return 1
	}

	action := args[0]
	configPath := config.DefaultConfigPath()

	switch action {
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tabby manage-group add <name>")
			return 1
		}
		name := args[1]
		if err := addGroup(configPath, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Printf("Added group: %s\n", name)

	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tabby manage-group delete <name>")
			return 1
		}
		name := args[1]
		if err := deleteGroup(configPath, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Printf("Deleted group: %s\n", name)

	case "rename":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: tabby manage-group rename <old-name> <new-name>")
			return 1
		}
		oldName := args[1]
		newName := args[2]
		if err := renameGroup(configPath, oldName, newName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Printf("Renamed group: %s -> %s\n", oldName, newName)

	case "set-color":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: tabby manage-group set-color <name> <color>")
			return 1
		}
		name := args[1]
		color := args[2]
		if err := setGroupColor(configPath, name, color); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Printf("Set color for group %s: %s\n", name, color)

	case "set-marker":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: tabby manage-group set-marker <name> <marker>")
			return 1
		}
		name := args[1]
		marker := args[2]
		if err := setGroupMarker(configPath, name, marker); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Printf("Set marker for group %s: %s\n", name, marker)

	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", action)
		return 1
	}
	return 0
}

func addGroup(configPath, name string) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	newGroup := config.DefaultGroupWithIndex(name, len(cfg.Groups))
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

	if existing := config.FindGroup(cfg, newName); existing != nil {
		return config.ErrGroupExists
	}

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

	group.Theme.Bg = color
	group.Theme.ActiveBg = ""
	group.Theme.Fg = ""
	group.Theme.ActiveFg = ""
	group.Theme.InactiveBg = ""
	group.Theme.InactiveFg = ""

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
