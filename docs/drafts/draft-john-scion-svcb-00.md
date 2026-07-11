---
title: "The scion SVCB Service Parameter and SCION-Aware Happy Eyeballs"
abbrev: "SCION SVCB"
category: std
docname: draft-john-scion-svcb-00
submissiontype: IETF
ipr: trust200902
area: "Internet"
workgroup: "Independent Submission"
keyword:
  - SVCB
  - SCION
  - Happy Eyeballs
  - path-aware networking
author:
  - fullname: Tony John
    initials: T.
    surname: John
    organization: Otto von Guericke University Magdeburg
    email: tonyjanugrah@gmail.com
normative:
  RFC2119:
  RFC8174:
  RFC9460:
informative:
  RFC8305:
  RFC9540:
  I-D.ietf-happy-happyeyeballs-v3:
  I-D.dekater-scion-dataplane:
  I-D.dekater-scion-controlplane:

--- abstract

This document defines the "scion" SvcParamKey for the SVCB and HTTPS DNS
resource record types {{RFC9460}}. The parameter conveys that a service
endpoint is additionally reachable over the SCION path-aware
internetworking architecture, and carries the SCION addresses at which
it is reachable. It further specifies how a client implementing Happy
Eyeballs Version 3 {{I-D.ietf-happy-happyeyeballs-v3}} incorporates
SCION-reachable endpoints — including multiple concurrently raced SCION
paths — into its candidate connection attempts alongside IPv6 and IPv4,
such that clients without SCION connectivity are entirely unaffected.

--- middle

# Introduction

SCION {{I-D.dekater-scion-dataplane}} {{I-D.dekater-scion-controlplane}}
is a path-aware internetworking architecture in which endpoints learn
multiple inter-domain paths to a destination and select among them.
Hosts deploying SCION are, in practice, dual-connected: they retain
ordinary IPv4/IPv6 connectivity while additionally being reachable over
SCION.

Today there is no standardized way for such a host to advertise its
SCION reachability in the DNS. Deployed practice uses a freeform TXT
record convention of the form "scion=ISD-AS,host" (see
{{txt-coexistence}}), which cannot participate in service binding:
it carries no association with ALPN protocols, ports, or the
SvcPriority machinery of {{RFC9460}}, and it is invisible to the
resolution phase of Happy Eyeballs Version 3
{{I-D.ietf-happy-happyeyeballs-v3}}, which is driven by SVCB/HTTPS
queries.

Meanwhile, Happy Eyeballs Version 3 (HEv3) defines its candidate
sorting and racing exclusively over IPv4 and IPv6 and provides no
extension point for additional network-layer protocols. Its two
accommodating surfaces are (a) the SVCB SvcParamKey registry, which is
the designated extensibility mechanism of the service binding
framework, and (b) the deliberately implementation-defined notion of
connection-attempt success (Section 6.1 of
{{I-D.ietf-happy-happyeyeballs-v3}}).

This document uses exactly those two surfaces:

1. It defines the "scion" SvcParamKey ({{scion-svcparam}}), modeled on
   the "ipv4hint"/"ipv6hint" parameters, carrying one or more SCION
   addresses for the service endpoint.

2. It specifies SCION-aware candidate construction, sorting, and racing
   for HEv3 clients ({{hev3}}), treating SCION as a third address
   family and expanding each SCION endpoint into a bounded set of
   per-path connection candidates.

A client that does not implement SCION ignores the parameter (per the
default SvcParam handling rules of {{RFC9460}}) and behaves exactly as
an unmodified HEv3 client.

# Conventions and Definitions

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in
BCP 14 {{RFC2119}} {{RFC8174}} when, and only when, they appear in all
capitals, as shown here.

ISD:
: Isolation Domain, the top-level grouping of SCION autonomous systems,
  identified by a 16-bit number.

ASN:
: A SCION AS number, a 48-bit number. Its canonical text form is either
  a decimal number (for values in the BGP-compatible range) or three
  colon-separated groups of up to four hexadecimal digits (e.g.
  "2:0:4a"); the "::" zero-compression of IPv6 is not used.

SCION address:
: The tuple of ISD, ASN, and a host address, written
  "ISD-ASN,host" (e.g. "71-2:0:4a,10.44.25.3" or
  "1-ff00:0:110,2001:db8::1").

Native SCION stack:
: A host configuration in which applications can open SCION
  connections directly (a SCION daemon and underlay connectivity are
  available), as opposed to reaching SCION through an IP-to-SCION
  translation gateway.

# The "scion" SvcParamKey {#scion-svcparam}

The "scion" SvcParamKey conveys that the service endpoint described by
a ServiceMode SVCB or HTTPS record is additionally reachable over
SCION, and enumerates the SCION addresses at which the endpoint is
reachable.

## Wire Format

The SvcParamValue is a non-empty sequence of one or more fixed-length
24-octet SCION address blocks:

~~~
+--------------------+------------------------+
| Field              | Length                 |
+--------------------+------------------------+
| ISD                | 2 octets, network order|
| ASN                | 6 octets, network order|
| Host address       | 16 octets              |
+--------------------+------------------------+
~~~

The host address field always contains an IPv6 address. An IPv4 host
address is carried as an IPv4-mapped IPv6 address (::ffff:a.b.c.d).

A SvcParamValue whose length is zero or is not a multiple of 24 octets
renders the RR malformed and it MUST be entirely ignored.

## Presentation Format {#presentation-format}

The presentation value is a comma-separated list ({{RFC9460}},
Appendix A.1) of SCION addresses in their canonical text form. Because
the SCION address text form itself contains a comma separating the
ISD-ASN from the host address, that inner comma MUST be escaped, in
the same manner that "alpn" values escape embedded commas:

~~~
example.com. 300 IN SVCB 1 . alpn=h3 port=443 (
                           scion=71-2:0:4a\,10.44.25.3 )
~~~

Zone-file implementations MUST accept both the decimal and hexadecimal
ASN text forms and MUST emit the form that was parsed, without
normalization, when round-tripping presentation data. Comparison of
ASNs for equality is performed on the wire-format value.

When converting a wire-format value to presentation form -- for
example when printing a record received on the wire -- no parsed text
form exists to preserve. In that case an implementation MUST render
ASNs of 2^32 and above in the hexadecimal group form and smaller ASNs
in decimal, matching SCION's canonical ASN text convention.

## Semantics

The "scion" SvcParamKey is only defined for ServiceMode records. In
AliasMode records recipients MUST ignore it, per Section 2.4.2 of
{{RFC9460}}.

Presence of the parameter indicates that the alternative endpoint
identified by the record's TargetName is reachable over SCION at each
of the listed SCION addresses. All other connection parameters of the
record apply to the SCION connection exactly as they apply to an IP
connection: in particular "port" designates the UDP or TCP port at the
SCION host address, "alpn"/"no-default-alpn" constrain the application
protocols offered on SCION connections, and "ech" applies to TLS
handshakes carried over SCION.

Like the "ipv4hint" and "ipv6hint" parameters, the listed addresses
are reachability information for the TargetName, not a security
assertion; see {{security}}.

The "scion" SvcParamKey SHOULD NOT be included in the "mandatory"
parameter. Marking it mandatory would cause clients without SCION
support to reject the entire record, defeating the incremental
deployment property that motivates this design. A zone operator MAY
mark it mandatory only in deployments where every intended client is
known to require SCION reachability information.

## Coexistence with the TXT Convention {#txt-coexistence}

Existing SCION deployments publish reachability using a TXT record of
the form:

~~~
example.com. 300 IN TXT "scion=71-2:0:4a,10.44.25.3"
~~~

This convention is consumed by IP-to-SCION translation gateways and
their DNS components, which synthesize AAAA answers inside a
SCION-mapped IPv6 prefix from it. This document does not deprecate the
TXT convention. During transition, zones SHOULD publish both the
"scion" SvcParam and the TXT record for a name, and when both are
published for the same name they MUST list the same set of SCION
addresses.

# Use in Happy Eyeballs Version 3 {#hev3}

This section extends the algorithm of
{{I-D.ietf-happy-happyeyeballs-v3}} for clients with SCION support. All
timer values of that document (Resolution Delay, Connection Attempt
Delay, and their bounds) apply unchanged. A client without SCION
support ignores this section entirely.

## Candidate Construction {#candidate-construction}

During hostname resolution (Section 4 of
{{I-D.ietf-happy-happyeyeballs-v3}}), a SCION-capable client obtains
"scion" SvcParamValues from the SVCB/HTTPS answers it already queries.
No additional DNS queries are introduced.

For each ServiceMode record carrying a "scion" parameter, and for each
SCION address listed, the client queries its local SCION path service
for paths to the address's ISD-ASN. The client selects up to K paths
(RECOMMENDED default: K=3), ranked by the client's path selection
policy; in the absence of a more specific policy, clients SHOULD rank
by path metadata latency where available and by path length (number of
hops) otherwise. Each selected path yields one connection candidate:
the tuple (SCION address, path, port, ALPN set).

A client whose host lacks a native SCION stack but that can reach
SCION through an IP-to-SCION translation gateway MAY instead
synthesize a single candidate per SCION address, dialing the
gateway-mapped IPv6 address as an ordinary IPv6 candidate. Such a
client MUST NOT also construct native per-path candidates for the same
address.

## Sorting

SCION is treated as a third address family in the sorting rules of
Section 5.3 of {{I-D.ietf-happy-happyeyeballs-v3}}: the "Preferred
Address Family Count" mechanism generalizes from two families (IPv6,
IPv4) to three (SCION, IPv6, IPv4).

When a native SCION stack is present, clients SHOULD order the first
SCION candidate before the first IP candidate, and thereafter
interleave families per the Preferred Address Family Count. Candidates
of the same SCION address on different paths are ordered by the path
ranking of {{candidate-construction}} and are separated by the
Connection Attempt Delay like any other successive candidates; a
failure of the leading path therefore costs one stagger interval
rather than a connection timeout.

Rationale for SCION-first ordering: the client possesses strictly more
information about the SCION candidates (explicit path metadata) than
about IP candidates, and SCION connection attempts exercise the
path-aware machinery this parameter exists to enable. Operators and
applications MAY configure IP-first ordering; the mechanism is
identical.

## Racing and Success

Connection attempts are launched and staggered exactly per Section 6
of {{I-D.ietf-happy-happyeyeballs-v3}}. For SCION candidates whose
ALPN set selects a QUIC-based protocol, the success condition (in the
sense of Section 6.1 of that document, which explicitly permits
higher-level state checks) is the completion of the QUIC handshake
over the SCION path of that candidate.

The first candidate to succeed — SCION or IP — causes cancellation of
all other in-flight and pending attempts. An attempt that fails before
its successor's timer fires promotes the next candidate immediately.

A client MUST be prepared for a SCION candidate to fail due to path
expiry or revocation between path lookup and connection attempt, and
MUST treat this as an ordinary candidate failure.

# Security Considerations {#security}

The "scion" SvcParamValue has the same trust properties as the
"ipv4hint" and "ipv6hint" parameters of {{RFC9460}}: it directs the
client's connection attempt but does not authenticate the endpoint.
Endpoint authentication is anchored, as for IP candidates, in the TLS
handshake performed against the certificate for the origin host name.
An attacker who can forge DNS answers can misdirect SCION connection
attempts, but gains nothing beyond what forging A/AAAA answers or IP
hints already yields; DNSSEC protects this parameter exactly as it
protects any other RR data.

Racing SCION against IP creates a potential downgrade surface: an
attacker able to delay or suppress SCION connectivity can steer
clients to IP paths (or vice versa). This is inherent to all Happy
Eyeballs style mechanisms and is bounded by the property that every
raced candidate terminates in the same authenticated TLS endpoint.
Clients with a policy requirement to use SCION exclusively (for
example, for path-control or compliance reasons) MUST NOT fall back to
IP candidates and SHOULD instead fail the connection; such a policy is
out of scope for this document.

The considerations of Section 6.3 of
{{I-D.ietf-happy-happyeyeballs-v3}} on pending security-relevant
SvcParams (such as "ech") under cryptographically protected DNS apply
unchanged to SCION candidates.

# IANA Considerations

IANA is requested to add the following entry to the "Service
Parameter Keys (SvcParamKeys)" registry created by {{RFC9460}}:

| Number | Name  | Meaning                            | Reference       |
|--------|-------|------------------------------------|-----------------|
| TBD1   | scion | SCION addresses for this endpoint  | (this document) |

Until a permanent codepoint is assigned, implementations of this
specification use the Private Use codepoint 65280 with the
presentation name "scion".

--- back

# Example Zone {#example-zone}

The following zone fragment illustrates a service published for
HTTP/3 and HTTP/2, dual-reachable over IP and SCION, together with the
coexisting legacy TXT record:

~~~
$ORIGIN scion.
@    3600 IN SOA  ns.scion. admin.scion. 2026071000 7200 3600 1209600 3600

web       IN AAAA 2001:db8:660::215
web       IN A    198.51.100.215
web       IN SVCB 1 . alpn=h3,h2 port=443 scion=1-150\,10.20.3.215
web       IN TXT  "scion=1-150,10.20.3.215"

games     IN SVCB 1 . alpn=h3 scion=71-2:0:4a\,10.44.25.3
games     IN TXT  "scion=71-2:0:4a,10.44.25.3"
~~~

# Test Vectors {#test-vectors}

Each vector gives the presentation form of one SCION address and the
exact 24-octet wire encoding of a "scion" SvcParamValue carrying it,
shown as ISD (2 octets) | ASN (6 octets) | host (16 octets); the
whitespace is illustrative only. The reference implementation executes
these vectors as its test suite.

~~~
Presentation: 1-150,10.20.3.215
Wire:         0001 000000000096 00000000000000000000ffff0a1403d7

Presentation: 1-ff00:0:110,2001:db8::1
Wire:         0001 ff0000000110 20010db8000000000000000000000001

Presentation: 71-2:0:4a,10.44.25.3
Wire:         0047 00020000004a 00000000000000000000ffff0a2c1903
~~~

A value carrying both of the first two addresses is the 48-octet
concatenation of their encodings, with the presentation form:

~~~
scion=1-150\,10.20.3.215,1-ff00:0:110\,2001:db8::1
~~~

Rendering wire values follows the canonical-ASN rule of
{{presentation-format}}: at the threshold, the ASN ffffffff
(2^32 - 1) renders as "4294967295" while 000100000000 (2^32) renders
as "1:0:0".

# Acknowledgments
{:numbered="false"}

This design was prototyped on the SCION testbed built for the IETF 126
hackathon. It builds on the deployed TXT-record convention and the
SCION-IP translation work of the NetSys group at Otto von Guericke
University Magdeburg.
