package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var version = "dev"

const defaultAddr = "127.0.0.1:8081"

func usage() {
	fmt.Fprint(os.Stdout, `cairo-ctl — management CLI for cairo-registry

Usage:
  cairo-ctl [flags] <subcommand> [args]

Subcommands:
  list              List all agents visible to --operator
  get <agent-id>    Show details for a single agent
  health            Show registry health status

Flags:
  --addr string     Admin listener address (default "127.0.0.1:8081")
  --operator string Caller identity sent as X-Operator-Identity header
  --version         Print version and exit

Note: --operator is required for list and get; omitting it returns empty results
(registry scopes by identity).
`)
}

func main() {
	args := os.Args[1:]
	addr := defaultAddr
	operator := ""

	for len(args) > 0 {
		a := args[0]
		if a == "--help" || a == "-h" {
			usage()
			os.Exit(0)
		}
		if a == "--version" {
			fmt.Println(version)
			os.Exit(0)
		}
		if strings.HasPrefix(a, "--addr=") {
			addr = strings.TrimPrefix(a, "--addr=")
			args = args[1:]
			continue
		}
		if a == "--addr" && len(args) > 1 {
			addr = args[1]
			args = args[2:]
			continue
		}
		if strings.HasPrefix(a, "--operator=") {
			operator = strings.TrimPrefix(a, "--operator=")
			args = args[1:]
			continue
		}
		if a == "--operator" && len(args) > 1 {
			operator = args[1]
			args = args[2:]
			continue
		}
		break
	}

	if len(args) == 0 {
		usage()
		os.Exit(0)
	}

	addr = strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")

	client := &http.Client{Timeout: 5 * time.Second}

	switch args[0] {
	case "list":
		cmdList(client, addr, operator)
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "error: get requires an agent-id argument")
			usage()
			os.Exit(1)
		}
		cmdGet(client, addr, operator, args[1])
	case "health":
		cmdHealth(client, addr)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown subcommand %q\n\n", args[0])
		usage()
		os.Exit(1)
	}
}

type agent struct {
	AgentID      string `json:"agent_id"`
	Hostname     string `json:"hostname"`
	Owner        string `json:"owner"`
	TailnetNode  string `json:"tailnet_node"`
	Version      string `json:"version"`
	RegisteredAt int64  `json:"registered_at"`
	LastSeenAt   int64  `json:"last_seen_at"`
	Status       string `json:"status"`
	WsConnected  int    `json:"ws_connected"`
}

type healthzResp struct {
	Status        string `json:"status"`
	Total         int64  `json:"total"`
	Active        int64  `json:"active"`
	Stale         int64  `json:"stale"`
	WsConnected   int64  `json:"ws_connected"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

func agoStr(unix int64) string {
	d := time.Since(time.Unix(unix, 0))
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func doGet(client *http.Client, url, operator string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if operator != "" {
		req.Header.Set("X-Operator-Identity", operator)
	}
	return client.Do(req)
}

func connErr(addr string) {
	fmt.Fprintf(os.Stderr, "could not connect to registry at %s — is it running?\n", addr)
	os.Exit(1)
}

func cmdList(client *http.Client, addr, operator string) {
	resp, err := doGet(client, "http://"+addr+"/agents", operator)
	if err != nil {
		connErr(addr)
	}
	defer resp.Body.Close()

	var agents []agent
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to decode response")
		os.Exit(1)
	}

	if len(agents) == 0 {
		fmt.Println("no agents found")
		return
	}

	fmt.Printf("%-36s\t%-20s\t%-30s\t%-8s\t%s\n", "AGENT_ID", "HOSTNAME", "OWNER", "STATUS", "LAST_SEEN")
	for _, a := range agents {
		fmt.Printf("%-36s\t%-20s\t%-30s\t%-8s\t%s\n",
			a.AgentID, a.Hostname, a.Owner, a.Status, agoStr(a.LastSeenAt))
	}
}

func cmdGet(client *http.Client, addr, operator, id string) {
	resp, err := doGet(client, "http://"+addr+"/agents/"+id, operator)
	if err != nil {
		connErr(addr)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintln(os.Stderr, "agent not found")
		os.Exit(1)
	}

	var a agent
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to decode response")
		os.Exit(1)
	}

	fmt.Printf("agent_id:      %s\n", a.AgentID)
	fmt.Printf("hostname:      %s\n", a.Hostname)
	fmt.Printf("owner:         %s\n", a.Owner)
	fmt.Printf("tailnet_node:  %s\n", a.TailnetNode)
	fmt.Printf("version:       %s\n", a.Version)
	fmt.Printf("status:        %s\n", a.Status)
	fmt.Printf("ws_connected:  %d\n", a.WsConnected)
	fmt.Printf("registered_at: %s\n", time.Unix(a.RegisteredAt, 0).UTC().Format(time.RFC3339))
	fmt.Printf("last_seen_at:  %s (%s)\n", time.Unix(a.LastSeenAt, 0).UTC().Format(time.RFC3339), agoStr(a.LastSeenAt))
}

func cmdHealth(client *http.Client, addr string) {
	resp, err := doGet(client, "http://"+addr+"/healthz", "")
	if err != nil {
		connErr(addr)
	}
	defer resp.Body.Close()

	var h healthzResp
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to decode response")
		os.Exit(1)
	}

	fmt.Printf("status:         %s\n", h.Status)
	fmt.Printf("total:          %d\n", h.Total)
	fmt.Printf("active:         %d\n", h.Active)
	fmt.Printf("stale:          %d\n", h.Stale)
	fmt.Printf("ws_connected:   %d\n", h.WsConnected)
	fmt.Printf("uptime_seconds: %d\n", h.UptimeSeconds)
}
