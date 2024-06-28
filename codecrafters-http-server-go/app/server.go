package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
)

/**
NOTES

nc -vz 127.0.0.1 4221
curl -v http://localhost:4221
curl -v http://localhost:4221/abcdefg
curl -v http://localhost:4221
curl -v http://localhost:4221/echo/abc
curl -v --header "User-Agent: foobar/1.2.3" http://localhost:4221/user-agent

## return file
./your_server.sh --directory /tmp/
echo -n 'Hello, World!' > /tmp/foo
curl -i http://localhost:4221/files/foo

## read request body
curl -v --data "12345" -H "Content-Type: application/octet-stream" http://localhost:4221/files/file_123

## compression headers
curl -v -H "Accept-Encoding: gzip" http://localhost:4221/echo/abc

## multiple compression
curl -v -H "Accept-Encoding: invalid-encoding-1, gzip, invalid-encoding-2" http://localhost:4221/echo/abc

## gzip compression
curl -v -H "Accept-Encoding: gzip" http://localhost:4221/echo/abc | hexdump -C

*/

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Println("Logs from your program will appear here!")

	var filePath string
	flag.StringVar(&filePath, "directory", "/tmp/", "path to folder")
	flag.Parse()

	listener, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		fmt.Println("failed to bind to port 4221")
		os.Exit(1)
	}

	userSignal := make(chan os.Signal, 1)
	signal.Notify(userSignal, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-userSignal
		fmt.Println("shutting down")
		listener.Close()
		os.Exit(0)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("error accepting connection: ", err.Error())
			break
		}

		go handleConn(conn, filePath)
	}

	// req := make([]byte, 1024)
	// conn.Read(req)

	// if !strings.HasPrefix(string(req), "GET / HTTP/1.1") {
	// 	conn.Write([]byte("HTTP/1.1 404 Not Found\r\n\r\n"))
	// 	return
	// }

	// conn.Write([]byte("HTTP/1.1 404 Not Found\r\n\r\n"))

}

func handleConn(conn net.Conn, filePath string) {
	defer conn.Close()

	req := make([]byte, 4096)
	n, err := conn.Read(req)
	if err != nil {
		fmt.Println("error reading request: ", err.Error())
		return
	}

	requestLineEnd := strings.Index(string(req[:n]), "\r\n")
	if requestLineEnd == -1 {
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	requestLine := string(req[:requestLineEnd])
	parts := strings.Fields(requestLine)
	if len(parts) < 2 {
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	method, path := parts[0], parts[1]

	fmt.Printf("received %s request for %s\n", method, path)

	listOfHeader := make(map[string]string)
	headersEnd := strings.Index(string(req[:n]), "\r\n\r\n")
	if headersEnd == -1 {
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	headersSection := string(req[requestLineEnd+2 : headersEnd])
	lines := strings.Split(headersSection, "\r\n")
	for _, line := range lines {
		headerParts := strings.SplitN(line, ":", 2)
		if len(headerParts) == 2 {
			listOfHeader[strings.TrimSpace(headerParts[0])] = strings.TrimSpace(headerParts[1])
		}
	}

	if path == "/" {
		responseBody := ""
		headers := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n", len(responseBody))
		conn.Write([]byte(headers + responseBody))
	} else if strings.HasPrefix(path, "/echo/") {
		echoStr := strings.TrimPrefix(path, "/echo/")
		responseBody := echoStr + "\n"

		theEncoding, ok := listOfHeader["Accept-Encoding"]
		if ok {
			encodings := strings.Split(theEncoding, ", ")
			if slices.Contains(encodings, "gzip") {
				var gzBuffer bytes.Buffer
				gz := gzip.NewWriter(&gzBuffer)
				if _, err := gz.Write([]byte(echoStr)); err != nil {
					fmt.Println("error gzip: ", err.Error())
					return
				}
				if err := gz.Close(); err != nil {
					fmt.Println("error gzip: ", err.Error())
					return
				}

				headers := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: %s\r\nContent-Length: %d\r\n\r\n",
					"gzip", len(gzBuffer.String()))
				conn.Write([]byte(headers + gzBuffer.String()))
				return
			}
		}

		headers := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n", len(echoStr))
		conn.Write([]byte(headers + responseBody))
	} else if strings.HasPrefix(path, "/user-agent") {
		userAgent, ok := listOfHeader["User-Agent"]
		if !ok {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			return
		}
		responseBody := userAgent + "\n"
		headers := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n", len(userAgent))
		conn.Write([]byte(headers + responseBody))
	} else if strings.HasPrefix(path, "/files/") {
		filename := strings.TrimPrefix(path, "/files/")
		fullPath := filePath + filename

		switch method {
		case "GET":
			data, err := os.ReadFile(fullPath)
			if err != nil {
				conn.Write([]byte("HTTP/1.1 404 Not Found\r\n\r\n"))
				return
			}

			headersResponse := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n", len(data))
			conn.Write([]byte(headersResponse))
			conn.Write(data)
		case "POST":
			body := string(req[headersEnd+4 : n])
			err = os.WriteFile(fullPath, []byte(body), 0644)
			if err != nil {
				conn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n\r\n"))
				return
			}

			responseBody := "File created successfully\n"
			headersResponse := fmt.Sprintf("HTTP/1.1 201 Created\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n", len(responseBody))
			conn.Write([]byte(headersResponse))
			conn.Write([]byte(responseBody))
		default:
			conn.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
			return
		}

	} else {
		responseBody := ""
		headers := fmt.Sprintf("HTTP/1.1 404 Not Found\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n", len(responseBody))
		conn.Write([]byte(headers + responseBody))
	}
}
