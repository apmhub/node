package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os/user"
	"strings"

	"github.com/zcalusic/sysinfo"
)

func main() {
	current, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	if current.Uid != "0" {
		log.Fatal("requires superuser privilege")
	}
	fmt.Println(current)
	var si sysinfo.SysInfo

	si.GetSysInfo()
	//fmt.Println(si.)
	data, err := json.MarshalIndent(&si, "", "  ")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(data))

	if strings.HasSuffix(si.Node.Hostname, ".armhub.local") {
		var disks []int
		for i := range si.Storage {
			disks = append(disks, int(si.Storage[i].Size))
		}
		m := Machine{
			IP:     "TODO",
			Prefix: "TODO",
			CPU:    si.CPU.Model,
			RamGB:  int(si.Memory.Size) / 1024,
			DiskGB: disks,
		}
		fmt.Println(m)
	}
}

type Machine struct {
	IP     string `json:"ip" db:"ip"`
	Prefix string `json:"prefix" db:"prefix"`
	CPU    string `json:"cpu" db:"cpu"`
	RamGB  int    `json:"ram_gb" db:"ram_gb"`
	DiskGB []int  `json:"disk_gb" db:"disk_gb"`
}
