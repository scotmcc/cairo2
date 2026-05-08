package main

import (
	"fmt"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

func runConfig(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: cairo config get <key> | cairo config set <key> <value>")
	}
	sub, key := args[0], args[1]

	database, err := sqliteopen.Open()
	if err != nil {
		return fmt.Errorf("open db: %v", err)
	}
	defer database.Close()

	switch sub {
	case "get":
		val, err := database.Config.Get(key)
		if err != nil {
			return err
		}
		if val == "" {
			fmt.Println("not set")
		} else {
			fmt.Println(val)
		}
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: cairo config set <key> <value>")
		}
		value := args[2]
		if err := database.Config.Set(key, value); err != nil {
			return err
		}
		fmt.Printf("set %s = %s\n", key, value)
	default:
		return fmt.Errorf("unknown subcommand %q: expected get or set", sub)
	}
	return nil
}
