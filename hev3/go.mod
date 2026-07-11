module github.com/netsys-lab/ietf-scion-testbed/hev3

go 1.26.4

require github.com/miekg/dns v0.0.0-00010101000000-000000000000

require (
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
)

replace github.com/miekg/dns => /home/tony/tjohn327/dns // TODO(pin): pseudo-version after push
