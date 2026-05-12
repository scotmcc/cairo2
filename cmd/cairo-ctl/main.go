package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
  list                              List all agents visible to --operator
  get <agent-id>                    Show details for a single agent
  health                            Show registry health status
  revoke <agent-id>                 Revoke an agent (marks status=revoked, rejects re-register)
  broadcast <command>               Persist a broadcast command to the registry command queue
  assign <agent-id> <type> [dept]   Set agent assignment (type: personal|departmental|enterprise)
  departments list                  List departments
  departments create <name>         Create a department
  departments members add <dept> <user> <role>    Add user to department
  departments members remove <dept> <user>        Remove user from department
  super-admins list                 List super-admins
  super-admins add <user>           Add super-admin
  super-admins remove <user>        Remove super-admin
  audit list [--gate G] [--actor A] [--action X]
             [--since DATE] [--until DATE] [--limit N] [--json]
                              List audit events (super-admin only)

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
	case "revoke":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "error: revoke requires an agent-id argument")
			usage()
			os.Exit(1)
		}
		cmdRevoke(client, addr, operator, args[1])
	case "broadcast":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "error: broadcast requires a command argument")
			usage()
			os.Exit(1)
		}
		cmdBroadcast(client, addr, operator, args[1])
	case "assign":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "error: assign requires <agent-id> <type> [dept]")
			usage()
			os.Exit(1)
		}
		dept := ""
		if len(args) > 3 {
			dept = args[3]
		}
		cmdAssign(client, addr, operator, args[1], args[2], dept)
	case "departments":
		cmdDepartments(client, addr, operator, args[1:])
	case "super-admins":
		cmdSuperAdmins(client, addr, operator, args[1:])
	case "audit":
		cmdAudit(client, addr, operator, args[1:])
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

func doPost(client *http.Client, addr, path, operator string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, body)
	if err != nil {
		return nil, err
	}
	if operator != "" {
		req.Header.Set("X-Operator-Identity", operator)
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}

func cmdRevoke(client *http.Client, addr, operator, agentID string) {
	resp, err := doPost(client, addr, "/agents/"+agentID+"/revoke", operator, nil)
	if err != nil {
		connErr(addr)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "error: agent %s not found\n", agentID)
		os.Exit(1)
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "error: unexpected status %d\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Printf("revoked %s\n", agentID)
}

func cmdBroadcast(client *http.Client, addr, operator, command string) {
	body, _ := json.Marshal(map[string]string{"command": command})
	resp, err := doPost(client, addr, "/broadcast", operator, bytes.NewReader(body))
	if err != nil {
		connErr(addr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		fmt.Fprintf(os.Stderr, "error: unexpected status %d\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Println("broadcast queued")
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

func doDelete(client *http.Client, addr, path, operator string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+path, nil)
	if err != nil {
		return nil, err
	}
	if operator != "" {
		req.Header.Set("X-Operator-Identity", operator)
	}
	return client.Do(req)
}

func cmdAssign(client *http.Client, addr, operator, agentID, agentType, dept string) {
	body, _ := json.Marshal(map[string]string{"agent_type": agentType, "dept_id": dept})
	resp, err := doPost(client, addr, "/agents/"+agentID+"/assign", operator, bytes.NewReader(body))
	if err != nil {
		connErr(addr)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
		os.Exit(1)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: unexpected status %d: %s\n", resp.StatusCode, strings.TrimSpace(string(b)))
		os.Exit(1)
	}
	fmt.Printf("assigned %s as %s", agentID, agentType)
	if dept != "" {
		fmt.Printf(" in %s", dept)
	}
	fmt.Println()
}

func cmdDepartments(client *http.Client, addr, operator string, args []string) {
	if len(args) == 0 || args[0] == "list" {
		resp, err := doGet(client, "http://"+addr+"/departments", operator)
		if err != nil {
			connErr(addr)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
			os.Exit(1)
		}
		var depts []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			CreatedAt int64  `json:"created_at"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&depts); err != nil {
			fmt.Fprintln(os.Stderr, "error: failed to decode response")
			os.Exit(1)
		}
		if len(depts) == 0 {
			fmt.Println("no departments found")
			return
		}
		fmt.Printf("%-32s\t%s\n", "ID", "NAME")
		for _, d := range depts {
			fmt.Printf("%-32s\t%s\n", d.ID, d.Name)
		}
		return
	}

	switch args[0] {
	case "create":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "error: departments create requires <name>")
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]string{"name": args[1]})
		resp, err := doPost(client, addr, "/departments", operator, bytes.NewReader(body))
		if err != nil {
			connErr(addr)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
			os.Exit(1)
		}
		if resp.StatusCode == http.StatusConflict {
			fmt.Fprintf(os.Stderr, "error: department %q already exists\n", args[1])
			os.Exit(1)
		}
		if resp.StatusCode != http.StatusCreated {
			fmt.Fprintf(os.Stderr, "error: unexpected status %d\n", resp.StatusCode)
			os.Exit(1)
		}
		fmt.Printf("created department %q\n", args[1])

	case "members":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "error: departments members requires add|remove")
			os.Exit(1)
		}
		switch args[1] {
		case "add":
			if len(args) < 5 {
				fmt.Fprintln(os.Stderr, "error: departments members add requires <dept> <user> <role>")
				os.Exit(1)
			}
			dept, user, role := args[2], args[3], args[4]
			body, _ := json.Marshal(map[string]string{"user": user, "role": role})
			resp, err := doPost(client, addr, "/departments/"+dept+"/members", operator, bytes.NewReader(body))
			if err != nil {
				connErr(addr)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden {
				fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
				os.Exit(1)
			}
			if resp.StatusCode == http.StatusNotFound {
				fmt.Fprintf(os.Stderr, "error: department %q not found\n", dept)
				os.Exit(1)
			}
			if resp.StatusCode != http.StatusCreated {
				b, _ := io.ReadAll(resp.Body)
				fmt.Fprintf(os.Stderr, "error: unexpected status %d: %s\n", resp.StatusCode, strings.TrimSpace(string(b)))
				os.Exit(1)
			}
			fmt.Printf("added %s to %s as %s\n", user, dept, role)

		case "remove":
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, "error: departments members remove requires <dept> <user>")
				os.Exit(1)
			}
			dept, user := args[2], args[3]
			resp, err := doDelete(client, addr, "/departments/"+dept+"/members/"+user, operator)
			if err != nil {
				connErr(addr)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden {
				fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
				os.Exit(1)
			}
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "error: unexpected status %d\n", resp.StatusCode)
				os.Exit(1)
			}
			fmt.Printf("removed %s from %s\n", user, dept)

		default:
			fmt.Fprintf(os.Stderr, "error: unknown departments members subcommand %q\n", args[1])
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "error: unknown departments subcommand %q\n", args[0])
		os.Exit(1)
	}
}

func cmdSuperAdmins(client *http.Client, addr, operator string, args []string) {
	if len(args) == 0 || args[0] == "list" {
		resp, err := doGet(client, "http://"+addr+"/super-admins", operator)
		if err != nil {
			connErr(addr)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
			os.Exit(1)
		}
		var users []string
		if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
			fmt.Fprintln(os.Stderr, "error: failed to decode response")
			os.Exit(1)
		}
		if len(users) == 0 {
			fmt.Println("no super-admins found")
			return
		}
		for _, u := range users {
			fmt.Println(u)
		}
		return
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "error: super-admins add requires <user>")
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]string{"user": args[1]})
		resp, err := doPost(client, addr, "/super-admins", operator, bytes.NewReader(body))
		if err != nil {
			connErr(addr)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
			os.Exit(1)
		}
		if resp.StatusCode != http.StatusCreated {
			fmt.Fprintf(os.Stderr, "error: unexpected status %d\n", resp.StatusCode)
			os.Exit(1)
		}
		fmt.Printf("added super-admin %s\n", args[1])

	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "error: super-admins remove requires <user>")
			os.Exit(1)
		}
		resp, err := doDelete(client, addr, "/super-admins/"+args[1], operator)
		if err != nil {
			connErr(addr)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
			os.Exit(1)
		}
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "error: unexpected status %d\n", resp.StatusCode)
			os.Exit(1)
		}
		fmt.Printf("removed super-admin %s\n", args[1])

	default:
		fmt.Fprintf(os.Stderr, "error: unknown super-admins subcommand %q\n", args[0])
		os.Exit(1)
	}
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

type auditEvent struct {
	Timestamp string            `json:"timestamp"`
	Actor     string            `json:"actor"`
	Gate      string            `json:"gate"`
	Action    string            `json:"action"`
	Target    string            `json:"target"`
	Decision  string            `json:"decision"`
	Reason    string            `json:"reason,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func cmdAudit(client *http.Client, addr, operator string, args []string) {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(os.Stderr, "error: audit requires 'list' subcommand")
		os.Exit(1)
	}
	args = args[1:]

	var (
		gateFilter   string
		actorFilter  string
		actionFilter string
		sinceFilter  string
		untilFilter  string
		limitFilter  string
		jsonOutput   bool
	)

	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--gate" && len(args) > 1:
			gateFilter = args[1]
			args = args[2:]
		case strings.HasPrefix(a, "--gate="):
			gateFilter = strings.TrimPrefix(a, "--gate=")
			args = args[1:]
		case a == "--actor" && len(args) > 1:
			actorFilter = args[1]
			args = args[2:]
		case strings.HasPrefix(a, "--actor="):
			actorFilter = strings.TrimPrefix(a, "--actor=")
			args = args[1:]
		case a == "--action" && len(args) > 1:
			actionFilter = args[1]
			args = args[2:]
		case strings.HasPrefix(a, "--action="):
			actionFilter = strings.TrimPrefix(a, "--action=")
			args = args[1:]
		case a == "--since" && len(args) > 1:
			sinceFilter = args[1]
			args = args[2:]
		case strings.HasPrefix(a, "--since="):
			sinceFilter = strings.TrimPrefix(a, "--since=")
			args = args[1:]
		case a == "--until" && len(args) > 1:
			untilFilter = args[1]
			args = args[2:]
		case strings.HasPrefix(a, "--until="):
			untilFilter = strings.TrimPrefix(a, "--until=")
			args = args[1:]
		case a == "--limit" && len(args) > 1:
			limitFilter = args[1]
			args = args[2:]
		case strings.HasPrefix(a, "--limit="):
			limitFilter = strings.TrimPrefix(a, "--limit=")
			args = args[1:]
		case a == "--json":
			jsonOutput = true
			args = args[1:]
		default:
			fmt.Fprintf(os.Stderr, "error: unknown flag %q\n", a)
			os.Exit(1)
		}
	}

	u := "http://" + addr + "/audit?"
	params := []string{}
	if gateFilter != "" {
		params = append(params, "gate="+gateFilter)
	}
	if actorFilter != "" {
		params = append(params, "actor="+actorFilter)
	}
	if actionFilter != "" {
		params = append(params, "action="+actionFilter)
	}
	if sinceFilter != "" {
		params = append(params, "since="+sinceFilter)
	}
	if untilFilter != "" {
		params = append(params, "until="+untilFilter)
	}
	if limitFilter != "" {
		params = append(params, "limit="+limitFilter)
	}
	u += strings.Join(params, "&")

	resp, err := doGet(client, u, operator)
	if err != nil {
		connErr(addr)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		fmt.Fprintln(os.Stderr, "error: forbidden (super-admin required)")
		os.Exit(1)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: unexpected status %d: %s\n", resp.StatusCode, strings.TrimSpace(string(b)))
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to read response")
		os.Exit(1)
	}

	if jsonOutput {
		fmt.Println(string(body))
		return
	}

	var events []auditEvent
	if err := json.Unmarshal(body, &events); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to decode response")
		os.Exit(1)
	}

	if len(events) == 0 {
		fmt.Println("no audit events found")
		return
	}

	fmt.Printf("%-20s  %-12s  %-8s  %-22s  %-20s  %-8s  %s\n",
		"TIMESTAMP", "ACTOR", "GATE", "ACTION", "TARGET", "DECISION", "REASON")
	for _, e := range events {
		ts := e.Timestamp
		if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			ts = t.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("%-20s  %-12s  %-8s  %-22s  %-20s  %-8s  %s\n",
			ts, e.Actor, e.Gate, e.Action, e.Target, e.Decision, e.Reason)
	}
}
