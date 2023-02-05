package tritonhttp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Server struct {
	// Addr specifies the TCP address for the server to listen on,
	// in the form "host:port". It shall be passed to net.Listen()
	// during ListenAndServe().
	Addr string // e.g. ":0"

	// VirtualHosts contains a mapping from host name to the docRoot path
	// (i.e. the path to the directory to serve static files from) for
	// all virtual hosts that this server supports
	VirtualHosts map[string]string
}

const (
	Proto          = "tcp"
	Host           = "localhost"
	Port           = "8080"
	MaxMessageSize = 101
	MaxRetries     = 3
)

const (
	responseProto = "HTTP/1.1"

	statusOK           = 200
	statusFileNotFound = 404
	statusBadRequest   = 400
)

var statusText = map[int]string{
	statusOK:           "OK",
	statusFileNotFound: "Not Found",
	statusBadRequest:   "Bad Request",
}

func listenForClientConnections(address string, hosts map[string]string) {

	fmt.Println("Starting " + Proto + " server on " + address)
	listener, err := net.Listen(Proto, address)
	if err != nil {
		fmt.Println("listen error")
		os.Exit(1)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println(("accept error"))
			//os.Exit(1)
			continue
		}

		go handleClientConnection(conn, hosts)
	}
}

func parseRequestLine(line string) (string, error) {
	fields := strings.SplitN(line, " ", 2)
	if len(fields) != 2 {
		return "", fmt.Errorf("could not parse the request line")
	}

	return fields[0], nil
}

func handleClientConnection(conn net.Conn, hosts_config map[string]string) {

	//defer conn.Close() Do not defer because it is persistenet connections
	for {
		// Set timeout
		if err := conn.SetReadDeadline(time.Now().Add(RECV_TIMEOUT)); err != nil {
			log.Printf("Failed to set timeout for connection %v", conn)
			_ = conn.Close()
			break
		}

		// Read next request from the client
		var read_buffer []byte
		response, err := ReadRequest(conn, &read_buffer, hosts_config)

		// resetting size  TODO : HAVE TO MOVE THIS BELOW
		read_buffer = make([]byte, 0)
		fmt.Println("length of read buffer", len(read_buffer))

		// Handle EOF
		if errors.Is(err, io.EOF) {
			log.Printf("Connection closed by %v", conn.RemoteAddr())
			_ = conn.Close()
			break
		}

		// timeout in this application means we just close the connection
		// Note : proj3 might require you to do a bit more here
		if err, ok := err.(net.Error); ok && err.Timeout() {
			log.Printf("Connection to %v timed out", conn.RemoteAddr())
			// writing 400 into new response and closing connection
			if len(read_buffer) != 0 {
				fmt.Println("Number of unread bytes in read_buffer", len(read_buffer))
				var response Response
				response.HandleBadRequest()
				err := response.Write(conn)
				if err != nil {
					fmt.Println("Error writing into conn", err)
				}
			}
			_ = conn.Close()
			break
		}

		fmt.Println("Writing into conn good request")
		fmt.Println("Response ", response)

		err = response.Write(conn)
		if err != nil {
			fmt.Println("Error writing into conn", err)
		}

	}
	fmt.Println("closing connection")

}

func ReadRequest(conn net.Conn, read_buffer *[]byte, hosts_config map[string]string) (resp Response, err error) {
	var response Response
	delimiter := []byte("\r\n\r\n")
	var full_request = read_buffer
	for {
		temp := make([]byte, 100)
		_, err := conn.Read(temp)

		//fmt.Println("Bytes read --> \n", bytes_read, string(temp))
		*full_request = append(*full_request, temp...)
		//time.Sleep(6 * time.Second)
		if bytes.Contains(temp, delimiter) { // break if delimiter exists
			
			break
		}
		if err != nil {
			fmt.Println("Error occured", err)
			return response, err
		}

	}

	response = parseRequest(*full_request, hosts_config)

	return response, nil
}

// HandleBadRequest prepares res to be a 405 Method Not allowed response
func (res *Response) HandleBadRequest() {
	fmt.Println("Handle bad request")
	res.Proto = responseProto
	res.StatusCode = statusBadRequest
	res.FilePath = ""
}

func (res *Response) HandleFileNotFound() {
	fmt.Println("Handle file not found")
	res.Proto = responseProto
	res.StatusCode = statusFileNotFound
	res.FilePath = ""
}

func parseRequest(requestBytes []byte, hosts_config map[string]string) Response {

	var response Response

	response.Proto = responseProto
	response.StatusCode = statusOK

	converted_req := string(requestBytes)
	fmt.Println("Full request is \n", converted_req)

	arr_lines := strings.Split(converted_req, "\r\n")
	//Removing 2 lines created by last delimiter
	arr_lines = arr_lines[0 : len(arr_lines)-2]

	if len(arr_lines) == 0 { //There should be at least one line
		response.HandleBadRequest()
		return response
	}

	var request Request

	status_code := validateHeaders(arr_lines, &request, &response)

	if status_code == statusBadRequest {
		fmt.Println("Bad request from validate headers")
		response.HandleBadRequest()
		return response
	}

	host := request.Host
	response.Request.Host = host

	checkFirstLineValid(arr_lines[0], &response, host, hosts_config)

	return response

}

func validateHeaders(allLines []string, request *Request, response *Response) (StatusCode int) {

	allLines = getHeaderLines(allLines)

	req_headers := make(map[string]string)

	for _, line := range allLines {
		//fmt.Println("Checking for header", line, strings.Contains(line, ":"))
		//line should contain ":"
		if !strings.Contains(line, ":") {
			fmt.Println("Missing :")
			return statusBadRequest
		}

		line_split := strings.Split(line, ":")
		if len(line_split) > 2 {
			fmt.Println("More than 2")
			return statusBadRequest
		}

		key := line_split[0]
		value := line_split[1]

		//Validate key
		is_valid_key := isAlphaNumHyphen(key)

		if !is_valid_key {
			return statusBadRequest
		}

		// Trimming value
		value = strings.Trim(value, " ")

		// Converting key to canonical form
		key = strings.ToLower(key)
		key = strings.Replace(key, "-", " ", -1)
		key = strings.Title(key)
		key = strings.Replace(key, " ", "-", -1)

		if key == "Host" {
			request.Host = value
		}
		if key == "Connection" {
			request.Close = value != "close"
		}

		req_headers[key] = value
	}

	request.Headers = req_headers
	request.Method = "GET"
	response.Request = request // Assigning request object in response

	fmt.Println("Req headers are", req_headers)

	return statusOK
}

func isAlphaNumHyphen(str string) bool {
	for _, c := range str {
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-') {
			fmt.Println("Invalid key", str)
			return false
		}
	}
	return true
}

func checkFirstLineValid(line string, response *Response, host string, hosts map[string]string) {

	// Checking for validity of number of spaces
	arr := strings.Split(line, " ")

	if len(arr) != 3 || (arr[0] != "GET") {
		fmt.Println(len(arr) != 3, arr[0] != "GET")
		fmt.Println("First line invalid")
		response.HandleBadRequest()
		return
	}

	// Checking for validity of URL

	url := arr[1]
	if url[0] != '/' {
		fmt.Println("Url not starting with /")
		response.HandleBadRequest()
		return
	}
	// Get doc root for specific host
	doc_root, exists := hosts[host]

	if !exists {
		response.HandleFileNotFound()
	}

	fmt.Println("Doc root for", host, "is", doc_root)

	file_path, status := validateURL(url, doc_root)
	response.FilePath = file_path
	response.StatusCode = status

}

func validateURL(url string, doc_root string) (cleaned_url string, status int) {
	index_file := "index.html"

	abs_path, err := filepath.Abs(doc_root)

	if err != nil {
		fmt.Println("Error getting absolute path", err)
		return
	}
	if url[len(url)-1] == '/' {
		url += index_file
	}

	file_path := filepath.Clean(abs_path + url)

	fmt.Println("The cleaned filepath is ", file_path)

	if file_path[:len(abs_path)] != abs_path {
		fmt.Println("Referencing out of root directory")
		return file_path, statusFileNotFound
	}

	_, err = os.Stat(file_path)
	// if os.IsNotExist(err) {
	// 	// file does not exist
	// 	fmt.Println("File does not exist")
	// 	return file_path, statusFileNotFound
	// } else
	if err != nil {
		// other error
		fmt.Println("error while getting requested file", err)
		return file_path, statusFileNotFound
	}

	return file_path, statusOK

}

func getHeaderLines(allLines []string) []string {
	header_lines := allLines[1:]
	return header_lines
}

// ListenAndServe listens on the TCP network address s.Addr and then
// handles requests on incoming connections.
func (s *Server) ListenAndServe() error {

	// Hint: Validate all docRoots

	// Hint: create your listen socket and spawn off goroutines per incoming client
	listenForClientConnections(s.Addr, s.VirtualHosts)

	panic("todo")

}

func (res *Response) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)

	statusLine := fmt.Sprintf("%v %v %v\r\n", res.Proto, res.StatusCode, statusText[res.StatusCode])
	if _, err := bw.WriteString(statusLine); err != nil {
		return err
	}

	res.Headers = make(map[string]string)

	fmt.Println("Request in response is", res.Request)

	res.Headers["Date"] = FormatTime(time.Now())
	if res.Request != nil {
		value, exists := res.Request.Headers["Connection"]
		fmt.Println("value of connection close", value)
		if exists {
			if value == "close" {
				res.Headers["Connection"] = "close"
			}
		}
	}

	if res.StatusCode == statusBadRequest {
		res.Headers["Connection"] = "close"
	}

	fmt.Println("status being received", res.StatusCode)
	if res.StatusCode == statusOK {

		file_info, err := os.Stat(res.FilePath)
		fmt.Println("File path being accessed", res.FilePath)
		if err != nil {
			// other error
			fmt.Println("Error accessing file", err)
			return err
		}
		res.Headers["Content-Type"] = MIMETypeByExtension(path.Ext(res.FilePath))

		res.Headers["Last-Modified"] = FormatTime(file_info.ModTime())
		res.Headers["Content-Length"] = strconv.Itoa((int(file_info.Size())))

		// write headers into buffer
		for key := range res.Headers {
			bw.WriteString(key + ": " + res.Headers[key] + "\r\n")
		}
		bw.WriteString("\r\n")

		fp, err := os.Open(res.FilePath)
		if err != nil {
			fmt.Println("Error accessing file in", getCurrentFunctionName(), err)
		}
		defer fp.Close()
		buf := make([]byte, 100)
		for {
			blen, err := fp.Read(buf)
			//fmt.Println("File contents -->", string(buf))

			if err != nil {
				if err != io.EOF {
					fmt.Println("Error reading file in ", getCurrentFunctionName(), err)
					return err
				}
				break
			}

			_, err = bw.Write(buf[:blen])

		}

	}

	if err := bw.Flush(); err != nil {
		return nil
	}

	return nil
}

func getCurrentFunctionName() string {
	pc, _, _, _ := runtime.Caller(1)
	funcName := runtime.FuncForPC(pc).Name()
	return funcName
}
