package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/zcalusic/sysinfo"
)

func main() {
	url := os.Getenv("API_URL")

	var si sysinfo.SysInfo

	si.GetSysInfo()
	//fmt.Println(si.)
	data, err := json.MarshalIndent(&si, "", "  ")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(data))

	var machineID int
	err = CreateMetric(machineID, si, url)
	if err != nil {
		fmt.Println("Error creating metric:", err)
	}
	return
	if !strings.HasSuffix(si.Node.Hostname, ".armhub.local") {
		machineID, err = CreateMachine(si, url)
		if err != nil {
			fmt.Println("Error creating machine:", err)
		}

		// TODO установка хостнейма
	} else {
		// TODO получение ID по хостнейму
	}

	err = CreateMetric(machineID, si, url)
	if err != nil {
		fmt.Println("Error creating metric:", err)
	}
}

// Функции епто

func CreateMachine(si sysinfo.SysInfo, url string) (int, error) {
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
		return 0, fmt.Errorf("error decode response: %s", err)
	}

	fmt.Printf("Machine created: ID=%d, Hostname=%s\n", result.ID, result.Hostname)
	return int(result.ID), nil
}

func CreateMetric(machineID int, si sysinfo.SysInfo, url string) error {
	freeMemMB, _ := GetFreeMemoryMB()
	cpuUsage, _ := GetCPUUsage()
	freeDiskGB, _ := GetDiskFreeGB()
	user, _ := GetLoggedInUser()
	uptime, _ := GetUptime()

	fmt.Println("Free RAM (MB):", freeMemMB)
	fmt.Println("CPU Usage (%):", cpuUsage)
	fmt.Println("Free Disk (GB):", freeDiskGB)
	fmt.Println("Logged user:", user)
	fmt.Println("Uptime (sec):", uptime)

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
	ActiveUser   string   `json:"active_user" db:"active_user"`
	MountedDisks []string `json:"mounted_disks" db:"mounted_disks"`
}

func GetFreeMemoryMB() (int, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	var memAvailableKB int

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			memAvailableKB, _ = strconv.Atoi(fields[1])
			break
		}
	}

	return memAvailableKB / 1024, nil
}

// 🔥 2. CPU (% загрузки)
func GetCPUUsage() (float64, error) {
	// читаем два раза /proc/stat
	idle, total := readCPU()

	if total == 0 {
		return 0, nil
	}

	usage := math.Round((1.0-float64(idle)/float64(total))*100*100) / 100
	return usage, nil
}

func readCPU() (idle, total uint64) {
	file, _ := os.Open("/proc/stat")
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Scan()
	fields := strings.Fields(scanner.Text())

	for i := 1; i < len(fields); i++ {
		val, _ := strconv.ParseUint(fields[i], 10, 64)
		total += val
		if i == 4 { // idle
			idle = val
		}
	}

	return
}

// 🔥 3. ДИСК (df)
func GetDiskFreeGB() (int, error) {
	cmd := exec.Command("df", "-B1", "/")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("invalid df output")
	}

	fields := strings.Fields(lines[1])
	freeBytes, _ := strconv.ParseInt(fields[3], 10, 64)

	gb := float64(freeBytes) / (1024 * 1024 * 1024)
	return int(math.Round(gb*100) / 100), nil
}

// 🔥 4. ПОЛЬЗОВАТЕЛЬ (GUI)
func GetLoggedInUser() (string, error) {
	cmd := exec.Command("who")
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	lines := strings.Split(out.String(), "\n")

	for _, line := range lines {
		// ищем DISPLAY (:0)
		if strings.Contains(line, ":0") {
			fields := strings.Fields(line)
			return fields[0], nil
		}
	}

	return "unknown", nil
}

// 🔥 5. АПТАЙМ (/proc/uptime)
func GetUptime() (int64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}

	fields := strings.Fields(string(data))
	uptimeFloat, _ := strconv.ParseFloat(fields[0], 64)

	return int64(uptimeFloat), nil
}
