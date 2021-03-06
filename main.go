// Copyright 2018 Eryx <evorui at gmail dot com>, All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hooto/hlog4g/hlog"
	"github.com/lessos/lessgo/encoding/json"
	"github.com/sysinner/incore/inconf"
	"github.com/sysinner/incore/inutils/filerender"
)

var (
	ssdb_prefix     = "/opt/ssdb/ssdb"
	ssdb_datadir    = ssdb_prefix + "/var"
	ssdb_bin_server = ssdb_prefix + "/bin/ssdb-server"
	ssdb_conf_init  = ssdb_prefix + "/etc/init_option.json"
	ssdb_conf       = ssdb_prefix + "/etc/ssdb.conf"
	ssdb_conf_tpl   = ssdb_prefix + "/etc/ssdb.conf.default"
	ssdb_cs_min     = int32(16)   // min cache size
	ssdb_cs_max     = int32(1024) // max cache size
	ssdb_wbs_min    = int32(8)    // min write buffer size
	ssdb_wbs_max    = int32(128)  // max write buffer size
	mu              sync.Mutex
	cfg_mu          sync.Mutex
	cfg_last        EnvConfig
	cfg_next        EnvConfig
	pgPodCfr        *inconf.PodConfigurator
)

type EnvConfigReplica struct {
	Id   uint32 `json:"id"`
	Host string `json:"host"`
	Port uint32 `json:"port"`
	Type string `json:"type"`
}

type EnvConfig struct {
	Inited   bool               `json:"inited"`
	RootAuth string             `json:"root_auth"`
	Resource EnvConfigResource  `json:"resource"`
	Updated  time.Time          `json:"updated"`
	Reps     []EnvConfigReplica `json:"reps"`
}

type EnvConfigResource struct {
	CacheSize       int32 `json:"cache_size"`
	WriteBufferSize int32 `json:"write_buffer_size"`
}

func (it *EnvConfig) RepEqual(ls []EnvConfigReplica) bool {

	if len(it.Reps) != len(ls) {
		return false
	}

	for _, v := range it.Reps {
		mat := false
		for _, v2 := range ls {
			if v.Id == v2.Id &&
				v.Type == v2.Type &&
				v.Host == v2.Host &&
				v.Port == v2.Port {

				mat = true
				break
			}
		}
		if !mat {
			return false
		}
	}

	return true
}

func main() {

	for {
		do()
		time.Sleep(10e9)
	}
}

func do() {

	fpbin, err := os.Open(ssdb_bin_server)
	if err != nil {
		hlog.Print("error", err.Error())
		return
	}
	fpbin.Close()

	var (
		tstart = time.Now()
		podCfr *inconf.PodConfigurator
		appCfr *inconf.AppConfigurator
		appCfg *inconf.AppConfigGroup
	)

	cfg_next = EnvConfig{}

	//
	{
		if pgPodCfr != nil {
			podCfr = pgPodCfr

			if !podCfr.Update() {
				return
			}

		} else {

			if podCfr, err = inconf.NewPodConfigurator(); err != nil {
				hlog.Print("error", err.Error())
				return
			}
		}

		appCfr = podCfr.AppConfigurator("sysinner-ssdb-*")
		if appCfr == nil {
			hlog.Print("error", "No AppSpec (sysinner-ssdb) Found")
			return
		}
		if appCfg = appCfr.AppConfigQuery("cfg/sysinner-ssdb"); appCfg == nil {
			hlog.Print("error", "No AppSpec (sysinner-ssdb) Found")
			return
		}

		if podCfr.PodSpec().Box.Resources.MemLimit > 0 {
			cfg_next.Resource.CacheSize = podCfr.PodSpec().Box.Resources.MemLimit
		}
	}

	{
		if v, ok := appCfg.ValueOK("server_auth"); ok {
			cfg_next.RootAuth = v.String()
		} else {
			hlog.Print("error", "No server/auth Found")
			return
		}

		//
		csize := ssdb_cs_min
		if v, ok := appCfg.ValueOK("cache_size"); ok {
			csize = (podCfr.Pod.Spec.Box.Resources.MemLimit * v.Int32()) / 100
			csize = csize / 10
		}
		if offset := csize % (8); offset > 0 {
			csize += offset
		}
		if csize < ssdb_cs_min {
			csize = ssdb_cs_min
		} else if csize > ssdb_cs_max {
			csize = ssdb_cs_max
		}
		cfg_next.Resource.CacheSize = csize

		//
		wbsize := ssdb_wbs_min
		if v, ok := appCfg.ValueOK("write_buffer_size"); ok {
			wbsize = v.Int32()
		}
		if wbsize > podCfr.Pod.Spec.Box.Resources.MemLimit/20 {
			wbsize = podCfr.Pod.Spec.Box.Resources.MemLimit / 20
		}
		if n := wbsize % (8); n > 0 {
			wbsize -= n
		}
		if wbsize < ssdb_wbs_min {
			wbsize = ssdb_wbs_min
		} else if wbsize > ssdb_wbs_max {
			wbsize = ssdb_wbs_max
		}
		cfg_next.Resource.WriteBufferSize = wbsize

		//
		reps := []EnvConfigReplica{}
		if srvPort := appCfr.AppService("sysinner-ssdb-mn", 0); srvPort != nil {
			for _, ep := range srvPort.Endpoints {
				if ep.Rep == podCfr.Pod.Replica.RepId {
					continue
				}
				reps = append(reps, EnvConfigReplica{
					Id:   ep.Rep,
					Type: "mirror",
					Host: ep.Ip,
					Port: ep.Port,
				})
			}
		}
		cfg_next.Reps = reps
	}

	//
	if cfg_last.RootAuth == "" {
		json.DecodeFile(ssdb_conf_init, &cfg_last)
	}

	//
	if err := init_cnf(); err != nil {
		hlog.Print("error", err.Error())
		return
	}

	if cfg_last.Resource.CacheSize != cfg_next.Resource.CacheSize ||
		cfg_last.Resource.WriteBufferSize != cfg_next.Resource.WriteBufferSize ||
		!cfg_last.RepEqual(cfg_next.Reps) {
		if err := restart(); err != nil {
			hlog.Print("error", err.Error())
			return
		}
		cfg_last.Resource.CacheSize = cfg_next.Resource.CacheSize
		cfg_last.Resource.WriteBufferSize = cfg_next.Resource.WriteBufferSize
		cfg_last.Reps = cfg_next.Reps

	} else {

		if err := restart(); err != nil {
			hlog.Print("error", err.Error())
			return
		}
	}

	hlog.Printf("info", "setup in %v", time.Since(tstart))
	pgPodCfr = podCfr
}

func init_cnf() error {

	if cfg_last.Inited &&
		cfg_last.Resource.CacheSize == cfg_next.Resource.CacheSize &&
		cfg_last.Resource.WriteBufferSize == cfg_next.Resource.WriteBufferSize &&
		cfg_last.RepEqual(cfg_next.Reps) {
		return nil
	}

	//
	var (
		cs  = cfg_next.Resource.CacheSize
		wbs = cfg_next.Resource.WriteBufferSize
	)
	sets := map[string]interface{}{
		"project_prefix":            ssdb_prefix,
		"server_auth":               cfg_next.RootAuth,
		"leveldb_cache_size":        fmt.Sprintf("%d", cs),
		"leveldb_write_buffer_size": fmt.Sprintf("%d", wbs),
		"leveldb_compression":       "no",
		"reps":                      cfg_next.Reps,
	}
	if len(cfg_next.Reps) > 0 {
		sets["rep_binlog_enable"] = "yes"
	} else {
		sets["rep_binlog_enable"] = "no"
	}

	if !cfg_last.Inited ||
		cfg_last.Resource.CacheSize != cfg_next.Resource.CacheSize ||
		cfg_last.Resource.WriteBufferSize != cfg_next.Resource.WriteBufferSize ||
		!cfg_last.RepEqual(cfg_next.Reps) {
		if err := filerender.Render(ssdb_conf_tpl, ssdb_conf, 0644, sets); err != nil {
			return err
		}
	}

	if !cfg_last.Inited {

		if err := filerender.Render(ssdb_conf_tpl, ssdb_conf, 0644, sets); err != nil {
			return err
		}

		cfg_last.Resource.CacheSize = cfg_next.Resource.CacheSize
		cfg_last.Resource.WriteBufferSize = cfg_next.Resource.WriteBufferSize
		cfg_last.Reps = cfg_next.Reps
	}

	cfg_last = cfg_next
	cfg_last.Inited = true

	return json.EncodeToFile(cfg_last, ssdb_conf_init, "  ")
}

func restart() error {

	mu.Lock()
	defer mu.Unlock()

	hlog.Printf("info", "start()")

	if !cfg_last.Inited {
		return errors.New("No Init")
	}

	if pidof() > 0 {
		return nil
	}

	args := []string{
		"-d",
		ssdb_conf,
		"-s",
		"restart",
	}
	_, err := exec.Command(ssdb_bin_server, args...).Output()
	if err != nil {
		hlog.Printf("error", "start server %s", err.Error())
	} else {
		hlog.Printf("info", "start server ok")
	}

	return err
}

func pidof() int {

	//
	for i := 0; i < 3; i++ {

		pidout, err := exec.Command("pgrep", "-f", ssdb_bin_server).Output()
		pid, _ := strconv.Atoi(strings.TrimSpace(string(pidout)))

		if err != nil || pid == 0 {
			time.Sleep(3e9)
			continue
		}

		return pid
	}

	return 0
}
