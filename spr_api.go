package block

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

import (
	"github.com/gorilla/mux"
)

var TEST_PREFIX = os.Getenv("TEST_PREFIX")

var UNIX_PLUGIN_LISTENER = TEST_PREFIX + "/state/dns/dns_block_plugin"
var CONFIG_PATH = TEST_PREFIX + "/state/dns/block_rules.json"

type ListEntry struct {
	URI     string
	Enabled bool
	Tags    []string //tags for which the list applies to
}

type DomainOverride struct {
	Type       string // Permit or Block
	Domain     string //
	ResultIP   string //ip to return
	ClientIP   string //target to apply to, '*' for all
	Expiration int64  //if non zero has unix time for when the entry should disappear
	Tags       []string
}

type SPRBlockConfig struct {
	BlockLists            []ListEntry //list of URIs with DNS block lists
	PermitDomains         []DomainOverride
	BlockDomains          []DomainOverride
	ClientIPExclusions    []string //these IPs should not have ad blocking
	RefreshSeconds        int
	QuarantineHostIP      string //for devices in quarantine mode
	RebindingCheckDisable bool
}

var Configmtx sync.Mutex

func (b *Block) loadSPRConfig() {
	Configmtx.Lock()
	defer Configmtx.Unlock()
	data, err := ioutil.ReadFile(CONFIG_PATH)
	err = json.Unmarshal(data, &b.config)
	if err != nil {
		fmt.Println(err)
	}
}

func (b *Block) saveConfig() {
	Configmtx.Lock()
	defer Configmtx.Unlock()

	//prune expired DomainOverrides here

	file, _ := json.MarshalIndent(b.config, "", " ")
	err := ioutil.WriteFile(CONFIG_PATH, file, 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func (b *Block) showConfig(w http.ResponseWriter, r *http.Request) {
	//reload
	b.loadSPRConfig()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(b.config)
}

func (b *Block) modifyOverrideDomains(w http.ResponseWriter, r *http.Request) {
	var overrides *[]DomainOverride = nil

	entry := DomainOverride{}
	err := json.NewDecoder(r.Body).Decode(&entry)

	if err == nil {
		if len(entry.Domain) == 0 || (entry.Domain[len(entry.Domain)-1:] != ".") {
			err = errors.New("domain should end in .")
		}
	}

	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if strings.ToLower(entry.Type) == "permit" {
		overrides = &b.config.PermitDomains
	} else if strings.ToLower(entry.Type) == "block" {
		overrides = &b.config.BlockDomains
	} else {
		http.Error(w, "Unexpected Override Type", 400)
		return
	}

	if entry.Expiration != 0 {
		//this specifies how many seconds in the future we should expire
		entry.Expiration = int64(entry.Expiration) + time.Now().Unix()
	}

	if r.Method == http.MethodPut {
		//check if the configuration already has this override, if so, replace it, otherwise, make a new one

		found := false
		for i, _ := range *overrides {
			if (*overrides)[i].Domain == entry.Domain {
				(*overrides)[i] = entry
				found = true
				break
			}
		}

		if !found {
			(*overrides) = append((*overrides), entry)
		}

		b.saveConfig()
	} else if r.Method == http.MethodDelete {
		found := -1
		for i, _ := range *overrides {
			if (*overrides)[i].Domain == entry.Domain {
				found = i
				break
			}
		}

		if found == -1 {
			http.Error(w, "Entry not found", 400)
			return
		}

		(*overrides) = append((*overrides)[:found], (*overrides)[found+1:]...)
		b.saveConfig()
	}

}

func (b *Block) quarantineHost(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var override string
		err := json.NewDecoder(r.Body).Decode(&override)

		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		ip := net.ParseIP(override)
		if ip == nil {
			err = errors.New("Invalid IP")
			http.Error(w, err.Error(), 400)
			return
		}
		b.config.QuarantineHostIP = override
		b.saveConfig()
	} else if r.Method == http.MethodDelete {
		b.config.QuarantineHostIP = ""
		b.saveConfig()
	}

}

var BLmtx sync.RWMutex

func (b *Block) modifyBlockLists(w http.ResponseWriter, r *http.Request) {

	entry := ListEntry{}

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		BLmtx.RLock()
		json.NewEncoder(w).Encode(b.config.BlockLists)
		BLmtx.RUnlock()
		return
	}

	err := json.NewDecoder(r.Body).Decode(&entry)

	if err == nil {
		if entry.URI == "" {
			err = errors.New("Need URI")
		}
	}

	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if r.Method == http.MethodPut {
		found := false
		BLmtx.Lock()
		for i, _ := range b.config.BlockLists {
			if b.config.BlockLists[i].URI == entry.URI {
				b.config.BlockLists[i].Enabled = entry.Enabled
				b.config.BlockLists[i].Tags = entry.Tags

				found = true
				break
			}
		}

		if !found {
			b.config.BlockLists = append(b.config.BlockLists, entry)
		}
		BLmtx.Unlock()

		b.saveConfig()
		//update the download
		b.download()
	} else if r.Method == http.MethodDelete {
		found := -1
		BLmtx.Lock()
		for i, _ := range b.config.BlockLists {
			if b.config.BlockLists[i].URI == entry.URI {
				found = i
				break
			}
		}

		if found == -1 {
			BLmtx.Unlock()
			http.Error(w, "Entry not found", 400)
			return
		}

		b.config.BlockLists = append(b.config.BlockLists[:found], b.config.BlockLists[found+1:]...)
		BLmtx.Unlock()
		b.saveConfig()
		b.download()

	}

	w.Header().Set("Content-Type", "application/json")
	BLmtx.RLock()
	json.NewEncoder(w).Encode(b.config.BlockLists)
	BLmtx.RUnlock()
}

func (b *Block) modifyExclusions(w http.ResponseWriter, r *http.Request) {

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(b.config.ClientIPExclusions)
		return
	}

	entry := ""
	err := json.NewDecoder(r.Body).Decode(&entry)

	if err == nil {
		if entry == "" {
			err = errors.New("Need IP Entry")
		}
	}

	ip := net.ParseIP(entry)
	if ip == nil {
		err = errors.New("Invalid IP")
	}

	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if r.Method == http.MethodPut {
		found := false
		for i, _ := range b.config.ClientIPExclusions {
			if b.config.ClientIPExclusions[i] == entry {
				found = true
				break
			}
		}

		if !found {
			b.config.ClientIPExclusions = append(b.config.ClientIPExclusions, entry)
		}

		b.saveConfig()
	} else if r.Method == http.MethodDelete {
		found := -1
		for i, _ := range b.config.ClientIPExclusions {
			if b.config.ClientIPExclusions[i] == entry {
				found = i
				break
			}
		}

		if found == -1 {
			http.Error(w, "Entry not found", 400)
			return
		}

		b.config.ClientIPExclusions = append(b.config.ClientIPExclusions[:found], b.config.ClientIPExclusions[found+1:]...)
		b.saveConfig()
	}

}

func (b *Block) getMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(gMetrics)
}

func (b *Block) setRefresh(w http.ResponseWriter, r *http.Request) {
	seconds := r.URL.Query().Get("seconds")
	i, err := strconv.Atoi(seconds)
	if err != nil || i < 0 {
		http.Error(w, "Error converting seconds", 400)
		return
	}

	b.config.RefreshSeconds = i
	b.saveConfig()
}

func (b *Block) setRebindingDisable(w http.ResponseWriter, r *http.Request) {
	value := r.URL.Query().Get("value")

	if strings.ToLower(value) == "true" {
		b.config.RebindingCheckDisable = true
	} else {
		b.config.RebindingCheckDisable = false
	}
	b.saveConfig()
}

func logRequest(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("DEBUGHTTP") != "" && r.URL.String() != "/healthy" {
			fmt.Printf("%s %s %s\n", r.RemoteAddr, r.Method, r.URL)
		}
		handler.ServeHTTP(w, r)
	})
}

func (b *Block) runAPI() {
	b.loadSPRConfig()

	unix_plugin_router := mux.NewRouter().StrictSlash(true)

	unix_plugin_router.HandleFunc("/config", b.showConfig).Methods("GET")
	unix_plugin_router.HandleFunc("/setRefresh", b.setRefresh).Methods("PUT")
	unix_plugin_router.HandleFunc("/disableRebinding", b.setRebindingDisable).Methods("PUT")
	unix_plugin_router.HandleFunc("/override", b.modifyOverrideDomains).Methods("PUT", "DELETE")
	unix_plugin_router.HandleFunc("/quarantineHost", b.quarantineHost).Methods("PUT", "DELETE")
	unix_plugin_router.HandleFunc("/blocklists", b.modifyBlockLists).Methods("GET", "PUT", "DELETE")
	unix_plugin_router.HandleFunc("/exclusions", b.modifyExclusions).Methods("GET", "PUT", "DELETE")
	unix_plugin_router.HandleFunc("/dump_domains", b.dumpEntries).Methods("GET")
	unix_plugin_router.HandleFunc("/metrics", b.getMetrics).Methods("GET")

	os.Remove(UNIX_PLUGIN_LISTENER)
	unixPluginListener, err := net.Listen("unix", UNIX_PLUGIN_LISTENER)
	if err != nil {
		panic(err)
	}

	pluginServer := http.Server{Handler: logRequest(unix_plugin_router)}

	pluginServer.Serve(unixPluginListener)
}
