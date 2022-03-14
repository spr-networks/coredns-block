package block

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

import (
	"github.com/gorilla/mux"
)

var TEST_PREFIX = "/tmp/"

var UNIX_PLUGIN_LISTENER = TEST_PREFIX + "/state/api/dns_plugin"
var CONFIG_PATH = TEST_PREFIX + "/state/dns/block_rules.json"

//  "StevenBlack": "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",

type ListEntry struct {
	URI     string
	Enabled bool
}

type DomainOverride struct {
	Domain     string //
	ResultIP   string //ip to return
	ClientIP   string //target to apply to, '*' for all
	Expiration int64  //if non zero has unix time for when the entry should disappear
}

type SPRBlockConfig struct {
	BlockLists         []ListEntry //list of URIs with DNS block lists
	PermitDomains      []DomainOverride
	BlockDomains       []DomainOverride
	ClientIPExclusions []string //these IPs should not have ad blocking
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

func (b *Block) updateOverrides(w http.ResponseWriter, r *http.Request, overrides *[]DomainOverride) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(*overrides)
		return
	}

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

	if entry.Expiration != 0 {
		//this specifies how many seconds in the future we should expire
		entry.Expiration = int64(entry.Expiration) + time.Now().Unix()
	}

	if r.Method == http.MethodPut {
		//check if the configuration already has this override, if so, replace it, otherwise, make a new one

		found := false
		for i, _ := range *overrides {
			if (*overrides)[i].Domain == entry.Domain {
				(*overrides)[i].ClientIP = entry.ClientIP
				(*overrides)[i].Expiration = entry.Expiration
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
func (b *Block) modifyBlockDomains(w http.ResponseWriter, r *http.Request) {
	b.updateOverrides(w, r, &b.config.BlockDomains)
}

func (b *Block) modifyPermitDomains(w http.ResponseWriter, r *http.Request) {
	b.updateOverrides(w, r, &b.config.PermitDomains)
}

func (b *Block) modifyBlockLists(w http.ResponseWriter, r *http.Request) {

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(b.config.BlockLists)
		return
	}

	entry := ListEntry{}
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
		for i, _ := range b.config.BlockLists {
			if b.config.BlockLists[i].URI == entry.URI {
				b.config.BlockLists[i].Enabled = entry.Enabled
				found = true
				break
			}
		}

		if !found {
			b.config.BlockLists = append(b.config.BlockLists, entry)
		}

		b.saveConfig()
		//update the download
		b.download()
	} else if r.Method == http.MethodDelete {
		found := -1
		for i, _ := range b.config.BlockLists {
			if b.config.BlockLists[i].URI == entry.URI {
				found = i
				break
			}
		}

		if found == -1 {
			http.Error(w, "Entry not found", 400)
			return
		}

		b.config.BlockLists = append(b.config.BlockLists[:found], b.config.BlockLists[found+1:]...)
		b.saveConfig()
		b.download()

	}

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

func logRequest(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("%s %s %s\n", r.RemoteAddr, r.Method, r.URL)
		handler.ServeHTTP(w, r)
	})
}

func (b *Block) runAPI() {
	b.loadSPRConfig()

	unix_plugin_router := mux.NewRouter().StrictSlash(true)

	unix_plugin_router.HandleFunc("/config", b.showConfig).Methods("GET")
	unix_plugin_router.HandleFunc("/block", b.modifyBlockDomains).Methods("GET", "PUT", "DELETE")
	unix_plugin_router.HandleFunc("/permit", b.modifyPermitDomains).Methods("GET", "PUT", "DELETE")
	unix_plugin_router.HandleFunc("/blocklists", b.modifyBlockLists).Methods("GET", "PUT", "DELETE")
	unix_plugin_router.HandleFunc("/exclusions", b.modifyExclusions).Methods("GET", "PUT", "DELETE")
	unix_plugin_router.HandleFunc("/dump_domains", b.dumpEntries).Methods("GET")

	os.Remove(UNIX_PLUGIN_LISTENER)
	unixPluginListener, err := net.Listen("unix", UNIX_PLUGIN_LISTENER)
	if err != nil {
		panic(err)
	}

	pluginServer := http.Server{Handler: logRequest(unix_plugin_router)}

	pluginServer.Serve(unixPluginListener)
}
