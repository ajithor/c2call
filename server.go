package main

import (
	"crypto/tls"
	"fmt"
	//"io"
	"log"
	"net/http"
	"time"
	"sync"
	"os"
	"io"

	"golang.org/x/net/http2"
	"github.com/chzyer/readline"
)

var	baseAudioTrack []byte

func audioCallHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("server: client connected")
	
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
	

	//so client doesnt block till who-knows-when go sends 
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var wg sync.WaitGroup

	//read from client (r.Body blocks until client writes)
	wg.Go(func() {
		buf := make([]byte, 1024)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				cmd, isFullCmd := decoderEngine(buf[:n])
				if isFullCmd=="true"{	//||isFullCmd=="none"{
					fmt.Println(cmd)
				}/*else{
					fmt.Println("full command recieved = ", isFullCmd)
				}*/
			}
			if err != nil {
				// io.EOF means client closed their write side
				return
			}
		}
	})

	// write to client every 20 ms
	wg.Go(func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done(): //return when client disconnects
				return
			case <-ticker.C: //triggers every 20 ms

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
				w.Header().Set("server", "nginx")
				w.Header().Set("date", time.Now().UTC().Format(http.TimeFormat))
				w.Header().Set("content-type", "audio/wav")
				w.Header().Set("cache-control", "no-cache")
				w.Header().Set("x-content-type-options", "nosniff")
				w.Header().Set("vary", "Accept-Encoding")
				w.Write(encodedChunk)
				flusher.Flush()
				index++
			}
		}
	})
	wg.Wait()
}

func main() {

	
	mux := http.NewServeMux()
	mux.HandleFunc("/call", audioCallHandler)

	srv := &http.Server{
		Addr:    ":443",
		Handler: mux,
		TLSConfig: &tls.Config{
			NextProtos: []string{"h2", "http/1.1"},
		},
	}
	http2.ConfigureServer(srv, &http2.Server{})

	log.Println("server: listening on :443")
	go StartCLIConsole()
	log.Fatal(srv.ListenAndServeTLS("server.crt", "server.key"))
}

// StartCLIConsole accepts asynchronous text inputs from operators
var rl *readline.Instance //so that my decoder can print without echoing past commands back
func StartCLIConsole() {
	fmt.Println("c2ne is active. Type a command and press Enter")
    rl, err := readline.New("c2ne> ")
    if err != nil {
        log.Fatal(err)
    }
    defer rl.Close()

    for {
        line, err := rl.Readline()
        if err == io.EOF || err == readline.ErrInterrupt {
            break
        }
        if err != nil {
            log.Println("read error:", err)
            continue
        }
        if line != "" {
			cliMessageChan <- line
		}
    }
}