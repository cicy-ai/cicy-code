package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type machineRecord struct {
	ID               int            `json:"id"`
	MachineKey       string         `json:"machine_key"`
	Label            string         `json:"label"`
	Host             string         `json:"host"`
	Port             int            `json:"port"`
	URL              string         `json:"url"`
	Token            string         `json:"token,omitempty"`
	Status           string         `json:"status"`
	LastSeenAt       sql.NullString `json:"-"`
	CapabilitiesJSON string         `json:"-"`
	CreatedAt        sql.NullString `json:"-"`
	UpdatedAt        sql.NullString `json:"-"`
}

type machineConfigNode struct {
	ID           string                 `json:"id"`
	MachineKey   string                 `json:"machine_key"`
	Label        string                 `json:"label"`
	Host         string                 `json:"host"`
	Port         int                    `json:"port"`
	URL          string                 `json:"url"`
	Token        string                 `json:"token"`
	Status       string                 `json:"status"`
	Online       bool                   `json:"online"`
	LastSeenAt   string                 `json:"last_seen_at"`
	Capabilities map[string]interface{} `json:"capabilities"`
}

type machineConfigFile struct {
	Default  string              `json:"default"`
	Machines []machineConfigNode `json:"machines"`
}

func machinesConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Private", "cicy-node.json")
}

func legacyMachinesConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Private", "cicy-nodes.json")
}

func machineCapabilitiesJSON(v interface{}) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 {
		return "{}"
	}
	return string(b)
}

func machineStatus(online bool, existing string) string {
	if online {
		return "online"
	}
	if existing != "" {
		return existing
	}
	return "offline"
}

func normalizeInstanceNode(node machineConfigNode) machineConfigNode {
	if node.MachineKey == "" {
		node.MachineKey = node.ID
	}
	if node.ID == "" {
		node.ID = node.MachineKey
	}
	if node.MachineKey == "" {
		node.MachineKey = node.URL
	}
	if node.ID == "" {
		node.ID = node.MachineKey
	}
	if node.Label == "" {
		node.Label = firstNonEmpty(node.MachineKey, node.ID, node.URL)
	}
	if node.Port == 0 {
		node.Port = 8008
	}
	if node.Status == "" {
		node.Status = machineStatus(node.Online, node.Status)
	}
	if node.LastSeenAt == "" && node.Online {
		node.LastSeenAt = time.Now().Format(time.RFC3339)
	}
	if node.Capabilities == nil {
		node.Capabilities = map[string]interface{}{}
	}
	if _, ok := node.Capabilities["runtime_kind"]; !ok {
		node.Capabilities["runtime_kind"] = "container"
	}
	return node
}

func instanceView(item M) M {
	view := M{}
	for k, v := range item {
		view[k] = v
	}
	view["instance_key"] = item["machine_key"]
	view["instance_id"] = firstNonEmpty(stringValue(item["instance_id"]), stringValue(item["machine_key"]), stringValue(item["id"]))
	view["instance_label"] = firstNonEmpty(stringValue(item["label"]), stringValue(item["machine_key"]))
	runtimeKind := "container"
	if caps, ok := item["capabilities"].(map[string]interface{}); ok {
		if rk := stringValue(caps["runtime_kind"]); rk != "" {
			runtimeKind = rk
		}
	}
	view["runtime_kind"] = runtimeKind
	return view
}

func loadMachineConfig() machineConfigFile {
	path := machinesConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		legacy := legacyMachinesConfigPath()
		legacyData, legacyErr := os.ReadFile(legacy)
		if legacyErr != nil {
			return machineConfigFile{Machines: []machineConfigNode{}}
		}
		var old struct {
			Default string                            `json:"default"`
			Nodes   map[string]map[string]interface{} `json:"nodes"`
		}
		if json.Unmarshal(legacyData, &old) != nil {
			return machineConfigFile{Machines: []machineConfigNode{}}
		}
		cfg := machineConfigFile{Default: old.Default, Machines: []machineConfigNode{}}
		for key, item := range old.Nodes {
			port := 8008
			if rawPort, ok := item["port"].(float64); ok {
				port = int(rawPort)
			}
			cfg.Machines = append(cfg.Machines, normalizeInstanceNode(machineConfigNode{
				ID:         key,
				MachineKey: key,
				Label:      stringValue(item["label"]),
				Host:       stringValue(item["host"]),
				Port:       port,
				URL:        stringValue(item["url"]),
				Token:      stringValue(item["token"]),
			}))
		}
		_ = saveMachineConfig(cfg)
		return cfg
	}
	var cfg machineConfigFile
	if json.Unmarshal(data, &cfg) != nil {
		return machineConfigFile{Machines: []machineConfigNode{}}
	}
	if cfg.Machines == nil {
		cfg.Machines = []machineConfigNode{}
	}
	for i := range cfg.Machines {
		cfg.Machines[i] = normalizeInstanceNode(cfg.Machines[i])
	}
	return cfg
}

func saveMachineConfig(cfg machineConfigFile) error {
	if cfg.Machines == nil {
		cfg.Machines = []machineConfigNode{}
	}
	for i := range cfg.Machines {
		cfg.Machines[i] = normalizeInstanceNode(cfg.Machines[i])
	}
	sort.Slice(cfg.Machines, func(i, j int) bool {
		return cfg.Machines[i].MachineKey < cfg.Machines[j].MachineKey
	})
	path := machinesConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func upsertMachine(machine machineConfigNode) (int64, error) {
	machine = normalizeInstanceNode(machine)
	res, err := store.Exec(store.Upsert("machines", "machine_key",
		[]string{"machine_key", "label", "host", "port", "url", "token", "status", "last_seen_at", "capabilities_json", "updated_at"},
		[]string{"label", "host", "port", "url", "token", "status", "last_seen_at", "capabilities_json", "updated_at"}),
		machine.MachineKey, machine.Label, machine.Host, machine.Port, machine.URL, machine.Token, machine.Status, nullableString(machine.LastSeenAt), machineCapabilitiesJSON(machine.Capabilities), time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		store.QueryRow("SELECT id FROM machines WHERE machine_key=?", machine.MachineKey).Scan(&id)
	}
	return id, nil
}

func syncMachinesFromConfig() ([]M, error) {
	cfg := loadMachineConfig()
	var synced []M
	for _, machine := range cfg.Machines {
		id, err := upsertMachine(machine)
		if err != nil {
			return nil, err
		}
		synced = append(synced, instanceView(M{"id": id, "machine_key": machine.MachineKey, "label": machine.Label, "url": machine.URL}))
	}
	if synced == nil {
		synced = []M{}
	}
	return synced, nil
}

func listMachines() ([]M, error) {
	log.Printf("[machines] list start")
	rows, err := store.Query("SELECT id, machine_key, label, host, port, url, token, status, last_seen_at, capabilities_json, created_at, updated_at FROM machines ORDER BY updated_at DESC, id DESC")
	if err != nil {
		log.Printf("[machines] query error: %v", err)
		return nil, err
	}
	defer rows.Close()
	var machines []M
	for rows.Next() {
		var rec machineRecord
		if err := rows.Scan(&rec.ID, &rec.MachineKey, &rec.Label, &rec.Host, &rec.Port, &rec.URL, &rec.Token, &rec.Status, &rec.LastSeenAt, &rec.CapabilitiesJSON, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			log.Printf("[machines] scan error: %v", err)
			return nil, err
		}
		item := M{
			"id":          rec.ID,
			"machine_key": rec.MachineKey,
			"label":       rec.Label,
			"host":        rec.Host,
			"port":        rec.Port,
			"url":         rec.URL,
			"token":       rec.Token,
			"status":      rec.Status,
		}
		if rec.LastSeenAt.Valid {
			item["last_seen_at"] = rec.LastSeenAt.String
		}
		if rec.CreatedAt.Valid {
			item["created_at"] = rec.CreatedAt.String
		}
		if rec.UpdatedAt.Valid {
			item["updated_at"] = rec.UpdatedAt.String
		}
		var caps map[string]interface{}
		if rec.CapabilitiesJSON != "" && rec.CapabilitiesJSON != "{}" && json.Unmarshal([]byte(rec.CapabilitiesJSON), &caps) == nil {
			item["capabilities"] = caps
		} else {
			item["capabilities"] = M{}
		}
		machines = append(machines, instanceView(item))
	}
	if err := rows.Err(); err != nil {
		log.Printf("[machines] rows error: %v", err)
		return nil, err
	}
	if machines == nil {
		machines = []M{}
	}
	log.Printf("[machines] list done count=%d", len(machines))
	return machines, nil
}

func findMachineByID(id string) (M, error) {
	var rec machineRecord
	err := store.QueryRow("SELECT id, machine_key, label, host, port, url, token, status, last_seen_at, capabilities_json, created_at, updated_at FROM machines WHERE id=?", id).
		Scan(&rec.ID, &rec.MachineKey, &rec.Label, &rec.Host, &rec.Port, &rec.URL, &rec.Token, &rec.Status, &rec.LastSeenAt, &rec.CapabilitiesJSON, &rec.CreatedAt, &rec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item := M{"id": rec.ID, "machine_key": rec.MachineKey, "label": rec.Label, "host": rec.Host, "port": rec.Port, "url": rec.URL, "token": rec.Token, "status": rec.Status}
	if rec.LastSeenAt.Valid {
		item["last_seen_at"] = rec.LastSeenAt.String
	}
	var caps map[string]interface{}
	if rec.CapabilitiesJSON != "" && json.Unmarshal([]byte(rec.CapabilitiesJSON), &caps) == nil {
		item["capabilities"] = caps
	} else {
		item["capabilities"] = M{}
	}
	return instanceView(item), nil
}

func handleMachines(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		machines, err := listMachines()
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"machines": machines, "instances": machines, "config_path": machinesConfigPath()})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleMachineRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req machineConfigNode
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	req = normalizeInstanceNode(req)
	if req.MachineKey == "" || req.URL == "" {
		httpErr(w, 400, "instance_key and url required")
		return
	}
	id, err := upsertMachine(req)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	cfg := loadMachineConfig()
	updated := false
	for i := range cfg.Machines {
		if cfg.Machines[i].MachineKey == req.MachineKey {
			cfg.Machines[i] = req
			updated = true
			break
		}
	}
	if !updated {
		cfg.Machines = append(cfg.Machines, req)
	}
	if cfg.Default == "" {
		cfg.Default = req.MachineKey
	}
	if err := saveMachineConfig(cfg); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true, "id": id, "machine_key": req.MachineKey, "instance_key": req.MachineKey, "config_path": machinesConfigPath()})
}

func handleMachineSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		RegistryURL    string `json:"registry_url"`
		RegistrySecret string `json:"registry_secret"`
	}
	readBody(r, &req)
	cfg := loadMachineConfig()
	if req.RegistryURL == "" {
		req.RegistryURL = os.Getenv("CICY_REGISTRY")
	}
	if req.RegistrySecret == "" {
		req.RegistrySecret = os.Getenv("CICY_REGISTRY_SECRET")
	}
	if req.RegistryURL != "" {
		nodes, err := fetchRegistryNodes(req.RegistryURL, req.RegistrySecret)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		seen := map[string]bool{}
		for _, node := range cfg.Machines {
			seen[node.MachineKey] = true
		}
		for _, node := range nodes {
			node = normalizeInstanceNode(node)
			if !seen[node.MachineKey] {
				cfg.Machines = append(cfg.Machines, node)
				seen[node.MachineKey] = true
			}
			_, _ = upsertMachine(node)
		}
	}
	if err := saveMachineConfig(cfg); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	synced, err := syncMachinesFromConfig()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true, "machines": synced, "instances": synced, "config_path": machinesConfigPath()})
}

func handleMachinePanes(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/machines/")
	id = strings.TrimSuffix(id, "/panes")
	id = strings.Trim(id, "/")
	if id == "" {
		httpErr(w, 400, "instance id required")
		return
	}
	rows, err := store.Query(`SELECT pane_id, title, role, agent_type, active, source_kind, source_ref FROM agent_config WHERE machine_id=? ORDER BY updated_at DESC, id DESC`, id)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	var panes []M
	for rows.Next() {
		var paneID, title, role, agentType, sourceKind, sourceRef sql.NullString
		var active sql.NullInt64
		rows.Scan(&paneID, &title, &role, &agentType, &active, &sourceKind, &sourceRef)
		panes = append(panes, M{
			"pane_id":     shortPaneID(paneID.String),
			"title":       title.String,
			"role":        role.String,
			"agent_type":  agentType.String,
			"active":      active.Int64,
			"source_kind": sourceKind.String,
			"source_ref":  sourceRef.String,
		})
	}
	if panes == nil {
		panes = []M{}
	}
	machine, err := findMachineByID(id)
	if err != nil {
		httpErr(w, 404, "instance not found")
		return
	}
	J(w, M{"machine": machine, "instance": machine, "panes": panes})
}

func fetchRegistryNodes(registryURL, secret string) ([]machineConfigNode, error) {
	url := strings.TrimRight(registryURL, "/") + "/nodes"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Nodes []map[string]interface{} `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	var nodes []machineConfigNode
	for _, item := range body.Nodes {
		port := 8008
		switch v := item["port"].(type) {
		case float64:
			port = int(v)
		case string:
			if p, err := strconv.Atoi(v); err == nil {
				port = p
			}
		}
		node := normalizeInstanceNode(machineConfigNode{
			ID:         firstNonEmpty(stringValue(item["instance_id"]), stringValue(item["id"])),
			MachineKey: firstNonEmpty(stringValue(item["instance_key"]), stringValue(item["machine_key"]), stringValue(item["instance_id"]), stringValue(item["id"])),
			Label:      firstNonEmpty(stringValue(item["instance_label"]), stringValue(item["label"])),
			Host:       stringValue(item["host"]),
			Port:       port,
			URL:        stringValue(item["url"]),
			Token:      stringValue(item["token"]),
			Status:     machineStatus(boolValue(item["online"]), stringValue(item["status"])),
			Online:     boolValue(item["online"]),
			LastSeenAt: stringValue(item["last_seen_at"]),
			Capabilities: map[string]interface{}{
				"source":       "registry",
				"runtime_kind": "container",
			},
		})
		nodes = append(nodes, node)
	}
	if nodes == nil {
		nodes = []machineConfigNode{}
	}
	return nodes, nil
}

func stringValue(v interface{}) string {
	s, _ := v.(string)
	return s
}

func boolValue(v interface{}) bool {
	b, ok := v.(bool)
	return ok && b
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nullableString(v string) interface{} {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
