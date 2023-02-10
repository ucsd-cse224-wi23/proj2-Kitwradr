package tritonhttp

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
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
		return
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println(("accept error"))
			//os.Exit(1)
			continue
		}
		fmt.Println("Creating a goroutine to service new request from ", conn.RemoteAddr().String())
		go handleClientConnection(conn, hosts)
	}
}

// func parseRequestLine(line string) (string, error) {
// 	fields := strings.SplitN(line, " ", 2)
// 	if len(fields) != 2 {
// 		return "", fmt.Errorf("could not parse the request line")
// 	}

// 	return fields[0], nil
// }

func handleClientConnection(conn net.Conn, hosts_config map[string]string) {

	//defer conn.Close() Do not defer because it is persistenet connections
	start := time.Now()
	br := bufio.NewReader(conn)
	for {
		//fmt.Println("coming in for loop")
		// Set timeout
		start := time.Now()
		fmt.Println("*************BEGIN*************")
		if err := conn.SetReadDeadline(time.Now().Add(RECV_TIMEOUT)); err != nil {
			fmt.Println("Failed to set timeout for connection", conn)
			_ = conn.Close()
			break
		}

		// Read next request from the client
		response, err, empty := ReadRequest(br, hosts_config)

		if err == io.EOF {
			fmt.Println("Connection closed by", conn.RemoteAddr())
			_ = conn.Close()
			break
		}

		// timeout in this application means we just close the connection
		// Note : proj3 might require you to do a bit more here
		if err, ok := err.(net.Error); ok && err.Timeout() {
			log.Printf("Connection to %v timed out", conn.RemoteAddr())
			// writing 400 into new response and closing connection
			if !empty {
				var response Response
				response.HandleBadRequest()
				err := response.Write(conn)
				if err != nil {
					fmt.Println("Error writing into conn", err)
				}
			}
			_ = conn.Close()
			fmt.Println("**************END**************")
			break
		}

		fmt.Println("Response ", response)

		if response.Request != nil && response.Request.Close {
			fmt.Println("Adding close header in Response")
			if response.Headers != nil {
				response.Headers["Connection"] = "close"
			}
		}

		err = response.Write(conn)
		if err != nil {
			fmt.Println("error occured writing response into connection buffer:", err)
		}

		if response.Request != nil && response.Request.Close {
			fmt.Println("Closing connection because of close header")
			conn.Close()
			fmt.Println("**************END**************")
			break
		}

		duration := time.Since(start)
		fmt.Println("Time elapsed for this request is -->", duration)
		fmt.Println("**************END**************")

	}
	duration := time.Since(start)
	fmt.Println("Time elapsed for closing connection", duration)
	fmt.Println("closing connection")

}

func ReadRequest2(br *bufio.Reader) (req string, err error) {

	var full_request string
	for {
		line, err := ReadLine(br)
		if err != nil {
			fmt.Println("Error reading line in ", getCurrentFunctionName(), err)
			if line == "" && err != io.EOF {
				//line is empty but still it is error since there is no complete request formed
				fmt.Println("line is empty but still it is error since there is no complete request formed")
				line = full_request
			}
			return line, err
		}
		if line == "" {
			// This marks header end
			fmt.Println("Encountered empty line")
			break
		}
		full_request += (line + "\r\n")
		fmt.Println("Read line from request", line)
	}

	//fmt.Println("Full request from ", getCurrentFunctionName(), "\n", full_request)

	return full_request, err
}

// returns if the buffer was empty when error occured
func ReadRequest(br *bufio.Reader, hosts_config map[string]string) (resp Response, err error, empty bool) {
	var response Response

	full_request, err := ReadRequest2(br) // when error occurs ReadRequest2 returns only the last read line

	if err != nil {
		fmt.Println("Last line(s) that was read when error occured is", full_request, "end")
		return response, err, full_request == ""
	}

	response = parseRequest([]byte(full_request), hosts_config)

	return response, err, false
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

	num_lines := len(arr_lines)

	fmt.Println("Number of arr_lines ", len(arr_lines))
	if num_lines == 0 || (num_lines == 1 && arr_lines[0] == "") {
		response.HandleBadRequest()
		return response
	}

	arr_lines = arr_lines[0 : len(arr_lines)-1] // there is one empty line after splitting by delimiter

	if len(arr_lines) == 0 { //There should be at least one line
		fmt.Println("No headers present")
		response.HandleBadRequest()
		return response
	}

	var request Request

	fmt.Println("Number of headers are ", len(arr_lines))

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
		fmt.Println("---Checking for header---\n", line)
		//line should contain ":"
		if !strings.Contains(line, ":") {
			fmt.Println("Missing :")
			return statusBadRequest
		}

		line_split := strings.Split(line, ":")
		if len(line_split) > 2 {
			fmt.Println("More than 2 items in header split")
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
			fmt.Println("Connection close header is present in the request")
			request.Close = (value == "close" || value == "Close")
		}

		req_headers[key] = value
		fmt.Println("---End of line--")
	}

	if request.Host == "" {
		fmt.Println("Host missing in headers")
		return statusBadRequest
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
	//fmt.Println("Valid key", str)
	return true
}

func checkFirstLineValid(line string, response *Response, host string, hosts map[string]string) {

	// Checking for validity of number of spaces
	arr := strings.Split(line, " ")

	if len(arr) != 3 || (arr[0] != "GET") {
		fmt.Println("First line invalid")
		response.HandleBadRequest()
		return
	}

	if arr[2] != "HTTP/1.1" {
		fmt.Println("First line invalid protocol")
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

	combined_path := abs_path + url
	fmt.Println("Absolute path is", abs_path, "\nCombined path is ", combined_path)

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

	return nil

}

func (res *Response) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)

	statusLine := fmt.Sprintf("%v %v %v\r\n", res.Proto, res.StatusCode, statusText[res.StatusCode])
	if _, err := bw.WriteString(statusLine); err != nil {
		fmt.Println("Error in writing status line into connection")
		return err
	}

	res.Headers = make(map[string]string)

	fmt.Println("Request in response is", res.Request)

	res.Headers["Date"] = FormatTime(time.Now())
	if res.Request != nil {
		if res.Request.Close {
			res.Headers["Connection"] = "close"
		}
	} else {
		fmt.Println("Request in response object is nil!")
	}

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
		sortAndWrite(res.Headers, bw)

		_, err = bw.WriteString("\r\n")
		if err != nil {
			return err
		}

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

			if err != nil {
				return err
			}

		}

	} else if res.StatusCode == statusBadRequest {

		//Not writing any other header only these 2

		_, err := bw.WriteString("Connection" + ": " + "close" + "\r\n")
		if err != nil {
			return err
		}

		_, err = bw.WriteString("Date" + ": " + res.Headers["Date"] + "\r\n\r\n") // adding one more \r\n in the end
		if err != nil {
			return err
		}

	} else if res.StatusCode == statusFileNotFound {
		sortAndWrite(res.Headers, bw)
		_, err := bw.WriteString("\r\n") // adding one more \r\n in the end
		if err != nil {
			return err
		}

	}

	if err := bw.Flush(); err != nil {
		return err
	}

	return nil
}

func getCurrentFunctionName() string {
	pc, _, _, _ := runtime.Caller(1)
	funcName := runtime.FuncForPC(pc).Name()
	return funcName
}

// ReadLine reads a single line ending with "\r\n" from br,
// striping the "\r\n" line end from the returned string.
// If any error occurs, data read before the error is also returned.
// You might find this function useful in parsing requests.
func ReadLine(br *bufio.Reader) (string, error) {
	var line string
	for {
		//log.Println("for loop in read line")
		s, err := br.ReadString('\n')
		//log.Println("line read", s, "size", br.Buffered())
		line += s
		// Return the error
		if err != nil {
			return line, err
		}
		// Return the line when reaching line end
		if strings.HasSuffix(line, "\r\n") {
			// Striping the line end
			line = line[:len(line)-2]
			return line, nil
		}
	}
}

func sortAndWrite(slice map[string]string, bw *bufio.Writer) (err error) {
	var keys []string
	for key := range slice {
		keys = append(keys, key)
	}

	// sort the keys in ascending order
	sort.Strings(keys)

	// create a new map from the sorted keys
	sortedMap := make(map[string]string)
	for _, key := range keys {
		sortedMap[key] = slice[key]
	}

	// print the sorted map
	for key, value := range sortedMap {
		_, err := bw.WriteString(key + ": " + value + "\r\n")
		if err != nil {
			return err
		}
	}
	return nil
}
