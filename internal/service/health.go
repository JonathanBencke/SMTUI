package service

import (
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

func defaultEnv() []string {
	return os.Environ()
}

func envPath() string {
	for _, e := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			return e[5:]
		}
	}
	return ""
}

func CheckPort(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
