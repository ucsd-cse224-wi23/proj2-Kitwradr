package tritonhttp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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

func ReadRequest(br *bufio.Reader) (req *Request, err error) {
	req = &Request{}

	// Read start line
	line, err := ReadLine(br)
	if err != nil {
		return nil, err
	}

	req.Method, err = parseRequestLine(line)

	if req.Method != "GET" {
		return nil, fmt.Errorf("invalid method")
	}

	for {
		line, err := ReadLine(br)
		if err != nil {
			print("Error in read line", err)
			return nil, err
		}

		print("Printing request on server ", line, "\n")

		if line == "" { // header end
			break
		}
	}

	return req, nil
}

func handleClientConnection(conn net.Conn, hosts_config map[string]string) {

	defer conn.Close()
	delimiter := []byte("\r\n\r\n")
	var full_request []byte
	for {
		temp := make([]byte, 100)
		bytes_read, err := conn.Read(temp)

		fmt.Println("Bytes read --> \n", bytes_read, string(temp))
		full_request = append(full_request, temp...)
		if bytes.Contains(temp, delimiter) {
			break
		}
		if err != nil {
			fmt.Println("Error occured", err)
			break
		}

	}

	response := parseRequest(full_request, hosts_config)

	fmt.Println("Writing into conn")
	fmt.Println("Response ", response)

	err := response.Write(conn)
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("closing connection")

}

// HandleBadRequest prepares res to be a 405 Method Not allowed response
func (res *Response) HandleBadRequest() {
	res.Proto = responseProto
	res.StatusCode = statusBadRequest
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

	status_code := validateAndGetHeaders(arr_lines, &request, &response)

	if status_code == statusBadRequest {
		fmt.Println("Bad request from validate headers")
		response.HandleBadRequest()
		return response
	}

	host := request.Host

	// check later if this is required
	// if len(header_lines) == 0 {
	// 	response.HandleBadRequest()
	// 	return response
	// }

	checkFirstLineValid(arr_lines[0], &response, host, hosts_config)

	return response

}

func validateAndGetHeaders(allLines []string, request *Request, response *Response) (StatusCode int) {

	allLines = getHeaderLines(allLines)

	req_headers := make(map[string]string)

	for _, line := range allLines {
		fmt.Println("Checking for header", line, strings.Contains(line, ":"))
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

		// Trimming key and value
		key = strings.Trim(key, " ")
		value = strings.Trim(key, " ")

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

	fmt.Println("Req headers are", req_headers)

	return statusOK
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

	fmt.Println("The hosts are", hosts)

	_, status := validateURL(url, "doc_root")

	response.StatusCode = status

}

func validateURL(url string, root_doc string) (cleaned_url string, status int) {

	if url == "/" {
		return "/index.html", statusOK
	}
	return "", statusOK

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

	if err := bw.Flush(); err != nil {
		return nil
	}

	return nil
}
