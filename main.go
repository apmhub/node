package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"
	"strings"

	"github.com/zcalusic/sysinfo"
)

func main() {
	url := os.Getenv("API_URL")

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

	if !strings.HasSuffix(si.Node.Hostname, ".armhub.local") {
		err = CreateMachine(si, url)
		if err != nil {
			fmt.Println("Error creating machine:", err)
		}
	}

	err = CreateMetric(si, url)
	if err != nil {
		fmt.Println("Error creating metric:", err)
	}
}

// Функции епто

func CreateMachine(si sysinfo.SysInfo, url string) error {
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

	url += "/machine"
	resp := Request(m, url)

	var result MachineCreateResponse

	err := json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return fmt.Errorf("error decode response: %s", err)
	}

	fmt.Printf("Machine created: ID=%d, Hostname=%s\n", result.ID, result.Hostname)
	return nil
}

func CreateMetric(si sysinfo.SysInfo, url string) error {
	// TODO
	return nil
}

// Запрос блять

func Request(bodyData any, url string) *http.Response {
	body, err := json.Marshal(bodyData)
	if err != nil {
		fmt.Println("error marshal:", err)
		return nil
	}

	resp, err := http.Post(
		url,
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		fmt.Println("error sending request:", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("unexpected status:", resp.Status)
		return nil
	}

	return resp
}

// Сущности нахуй

type Machine struct {
	IP     string `json:"ip" db:"ip"`
	Prefix string `json:"prefix" db:"prefix"`
	CPU    string `json:"cpu" db:"cpu"`
	RamGB  int    `json:"ram_gb" db:"ram_gb"`
	DiskGB []int  `json:"disk_gb" db:"disk_gb"`
}

type MachineCreateResponse struct {
	ID       int64  `json:"id"`
	Hostname string `json:"hostname"`
}

type Metric struct {
	MachineID    int64    `json:"machine_id" db:"machine_id"`
	Uptime       int64    `json:"uptime" db:"uptime"`
	CpuLoad      float32  `json:"cpu_load" db:"cpu_load"`
	FreeMemoryMB int      `json:"free_memory_mb" db:"free_memory_mb"`
	DiskFreeGB   int      `json:"disk_free_gb" db:"disk_free_gb"`
	CurrentUser  string   `json:"current_user" db:"current_user"`
	MountedDisks []string `json:"mounted_disks" db:"mounted_disks"`
	CreatedAt    string   `json:"created_at" db:"created_at"`
}
