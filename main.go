package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/zcalusic/sysinfo"
)

// Метрики Prometheus
var (
	cpuUsage = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_cpu_usage_percent",
		Help: "CPU usage percent",
	})
	memFree = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_memory_free_mb",
		Help: "Free memory in MB",
	})
	diskFree = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_disk_free_gb",
		Help: "Free disk space on / in GB",
	})
	uptimeSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_uptime_seconds",
		Help: "System uptime in seconds",
	})
)

func main() {
	if err := godotenv.Load(".env"); err != nil {
		log.Println("No .env file found")
	}

	apiURL := os.Getenv("API_URL")
	domain := os.Getenv("DOMAIN")
	port := os.Getenv("PORT")
	if port == "" {
		port = "9100"
	}
	fmt.Printf("url: %s, domain: %s, port: %s\n", apiURL, domain, port)

	var si sysinfo.SysInfo
	si.GetSysInfo()

	// Регистрация — бесконечный retry с backoff
	machineID := register(si, apiURL, domain)

	// Поднимаем HTTP-сервер
	prometheus.MustRegister(cpuUsage, memFree, diskFree, uptimeSeconds)

	mux := newMux(machineID, si)
	srv := newServer(port, mux)

	// Фоновый сбор метрик каждые 15 секунд
	go collectMetrics()

	fmt.Printf("Listening on :%s\n", port)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Println("Server error:", err)
		os.Exit(1)
	}
}

// register пытается зарегистрировать машину бесконечно с exponential backoff.
func register(si sysinfo.SysInfo, apiURL, domain string) int64 {
	backoff := 5 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		var machineID int64
		var err error

		if !strings.HasSuffix(si.Node.Hostname, "."+domain) {
			var machine *MachineCreateResponse
			machine, err = CreateMachine(si, apiURL)
			if err == nil {
				machineID = machine.ID
			}
		} else {
			machineID, err = GetMachineIDByHostname(si.Node.Hostname, apiURL)
		}

		if err == nil && machineID != 0 {
			fmt.Printf("Registered with machine ID: %d\n", machineID)
			return machineID
		}

		fmt.Printf("Registration failed (%v), retry in %s\n", err, backoff)
		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// collectMetrics обновляет Prometheus-метрики каждые 15 секунд.
func collectMetrics() {
	for {
		if cpu, err := GetCPUUsage(); err == nil {
			cpuUsage.Set(float64(cpu))
		}
		if mem, err := GetFreeMemoryMB(); err == nil {
			memFree.Set(float64(mem))
		}
		if disk, err := GetDiskFreeGB(); err == nil {
			diskFree.Set(float64(disk))
		}
		if up, err := GetUptime(); err == nil {
			uptimeSeconds.Set(float64(up))
		}
		time.Sleep(15 * time.Second)
	}
}

// newMux создаёт маршрутизатор с двумя эндпоинтами.
func newMux(machineID int64, si sysinfo.SysInfo) *http.ServeMux {
	mux := http.NewServeMux()

	// /metrics — Prometheus scrape endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// /info — медленно меняющиеся данные (IP, MAC, диски, пользователь)
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		ip, mac, _ := GetIPAndMAC()
		disks, _ := GetMountedDisks()
		user, _ := GetLoggedInUser()

		info := InfoResponse{
			MachineID:    machineID,
			IP:           ip,
			MAC:          mac,
			MountedDisks: disks,
			ActiveUser:   user,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	})

	return mux
}

// newServer создаёт HTTP-сервер. Сюда потом добавится tls.Config для mTLS.
func newServer(port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
}

// newClient создаёт HTTP-клиент для регистрации. Сюда потом добавится tls.Config для mTLS.
func newClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// ─── Регистрация машины ────────────────────────────────────────────────────

func CreateMachine(si sysinfo.SysInfo, apiURL string) (*MachineCreateResponse, error) {
	var totalDisk int
	for i := range si.Storage {
		totalDisk += int(si.Storage[i].Size)
	}
	ip, mac, err := GetIPAndMAC()
	if err != nil {
		return nil, fmt.Errorf("error getting IP and MAC: %w", err)
	}

	m := Machine{
		IP:     ip,
		MAC:    mac,
		Prefix: "TODO",
		CPU:    si.CPU.Model,
		RamGB:  int(si.Memory.Size) / 1024,
		DiskGB: totalDisk,
	}

	resp := Request(m, apiURL+"/machine")
	if resp == nil {
		return nil, fmt.Errorf("no response from server")
	}
	defer resp.Body.Close()

	var result MachineCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	fmt.Printf("Machine created: ID=%d, Hostname=%s\n", result.ID, result.Hostname)
	return &result, nil
}

func GetMachineIDByHostname(hostname, apiURL string) (int64, error) {
	u, err := url.Parse(apiURL + "/machine/id")
	if err != nil {
		return 0, err
	}
	q := u.Query()
	q.Set("hostname", hostname)
	u.RawQuery = q.Encode()

	client := newClient()
	resp, err := client.Get(u.String())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bad status: %s", resp.Status)
	}

	var result ID
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.ID, nil
}

// ─── HTTP-запрос ──────────────────────────────────────────────────────────

func Request(bodyData any, url string) *http.Response {
	body, err := json.Marshal(bodyData)
	if err != nil {
		fmt.Println("error marshal:", err)
		return nil
	}

	client := newClient()
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Println("error sending request:", err)
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Println("unexpected status:", resp.Status)
		resp.Body.Close()
		return nil
	}

	return resp
}

// ─── Типы ─────────────────────────────────────────────────────────────────

type Machine struct {
	IP     string `json:"ip"`
	MAC    string `json:"mac"`
	Prefix string `json:"prefix"`
	CPU    string `json:"cpu"`
	RamGB  int    `json:"ram_gb"`
	DiskGB int    `json:"disk_gb"`
}

type MachineCreateResponse struct {
	ID       int64  `json:"id"`
	Hostname string `json:"hostname"`
}

type InfoResponse struct {
	MachineID    int64    `json:"machine_id"`
	IP           string   `json:"ip"`
	MAC          string   `json:"mac"`
	MountedDisks []string `json:"mounted_disks"`
	ActiveUser   string   `json:"active_user"`
}

type ID struct {
	ID int64 `json:"id"`
}

// ─── Системные метрики ────────────────────────────────────────────────────

func GetIPAndMAC() (string, string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", "", err
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			return ip.String(), iface.HardwareAddr.String(), nil
		}
	}

	return "", "", fmt.Errorf("no active interface found")
}

func GetFreeMemoryMB() (int, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			kb, _ := strconv.Atoi(fields[1])
			return kb / 1024, nil
		}
	}
	return 0, fmt.Errorf("MemAvailable not found")
}

func GetCPUUsage() (float32, error) {
	idle, total := readCPU()
	if total == 0 {
		return 0, nil
	}
	usage := float32(math.Round((1.0-float64(idle)/float64(total))*100*100) / 100)
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
		if i == 4 {
			idle = val
		}
	}
	return
}

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

func GetLoggedInUser() (string, error) {
	cmd := exec.Command("who")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, ":0") {
			fields := strings.Fields(line)
			return fields[0], nil
		}
	}
	return "unknown", nil
}

func GetUptime() (int64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	uptimeFloat, _ := strconv.ParseFloat(fields[0], 64)
	return int64(uptimeFloat), nil
}

func GetMountedDisks() ([]string, error) {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var mounts []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		fstype := fields[2]
		// Только реальные файловые системы
		if fstype == "ext4" || fstype == "xfs" || fstype == "btrfs" || fstype == "vfat" || fstype == "ntfs" {
			mounts = append(mounts, fields[1])
		}
	}
	return mounts, nil
}
