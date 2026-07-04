package shape

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"syscall"

	"github.com/vishvananda/netlink"
)

type netlinkShaper struct{}

func NewNetlinkShaper() Shaper { return netlinkShaper{} }

func qdiscAttrs(link netlink.Link) netlink.QdiscAttrs {
	return netlink.QdiscAttrs{
		LinkIndex: link.Attrs().Index,
		Handle:    netlink.MakeHandle(1, 0),
		Parent:    netlink.HANDLE_ROOT,
	}
}

func (netlinkShaper) Apply(dev string, p Params) error {
	link, err := netlink.LinkByName(dev)
	if err != nil {
		return fmt.Errorf("device %s: %w", dev, err)
	}
	attrs := netlink.NetemQdiscAttrs{Limit: 10000}
	if p.DelayMs != nil {
		attrs.Latency = uint32(*p.DelayMs * 1000) // microseconds
	}
	if p.JitterMs != nil {
		attrs.Jitter = uint32(*p.JitterMs * 1000)
	}
	if p.LossPct != nil {
		attrs.Loss = float32(*p.LossPct)
	}
	qd := netlink.NewNetem(qdiscAttrs(link), attrs)
	if p.RateMbit != nil {
		qd.Rate64 = uint64(*p.RateMbit * 1e6 / 8) // netem rate is bytes/sec
	}
	return netlink.QdiscReplace(qd)
}

func (netlinkShaper) Get(dev string) (Params, error) {
	link, err := netlink.LinkByName(dev)
	if err != nil {
		return Params{}, fmt.Errorf("device %s: %w", dev, err)
	}
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return Params{}, err
	}
	for _, q := range qds {
		ne, ok := q.(*netlink.Netem)
		if !ok || ne.Parent != netlink.HANDLE_ROOT {
			continue
		}
		p := Params{}
		if ne.Latency > 0 {
			v := float64(ne.Latency) / 1000
			p.DelayMs = &v
		}
		if ne.Jitter > 0 {
			v := float64(ne.Jitter) / 1000
			p.JitterMs = &v
		}
		// Write-path NetemQdiscAttrs.Loss is a float32 percent, but the
		// read-path Netem.Loss here is the kernel's raw u32 probability
		// (0..math.MaxUint32 == 0..100%), so it must be rescaled.
		if ne.Loss > 0 {
			v := float64(ne.Loss) / float64(math.MaxUint32) * 100
			p.LossPct = &v
		}
		if ne.Rate64 > 0 {
			v := float64(ne.Rate64) * 8 / 1e6
			p.RateMbit = &v
		}
		return p, nil
	}
	return Params{}, nil // no netem: unshaped
}

func (netlinkShaper) Clear(dev string) error {
	link, err := netlink.LinkByName(dev)
	if err != nil {
		return fmt.Errorf("device %s: %w", dev, err)
	}
	qd := netlink.NewNetem(qdiscAttrs(link), netlink.NetemQdiscAttrs{})
	if err := netlink.QdiscDel(qd); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func isNotFound(err error) bool {
	// Netlink surfaces syscall.Errno values. Tolerate both ENOENT and
	// EINVAL: the kernel returns EINVAL when deleting a non-existent root
	// qdisc on some paths.
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EINVAL)
}

// DevByAddr returns the network device that owns ip.
func DevByAddr(ip netip.Addr) (string, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return "", err
	}
	for _, l := range links {
		addrs, err := netlink.AddrList(l, netlink.FAMILY_ALL)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			got, ok := netip.AddrFromSlice(a.IP)
			if ok && got.Unmap() == ip {
				return l.Attrs().Name, nil
			}
		}
	}
	return "", fmt.Errorf("no device has address %s", ip)
}
