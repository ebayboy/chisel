package tunnel

import (
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/jpillora/chisel/share/cio"
	"github.com/jpillora/chisel/share/cnet"
	"github.com/jpillora/chisel/share/settings"
	"github.com/jpillora/sizestr"
	"golang.org/x/crypto/ssh"
)

// * 健康检查处理函数：
func (t *Tunnel) handleSSHRequests(reqs <-chan *ssh.Request) {
	for r := range reqs {
		switch r.Type {
		case "ping":
			t.Debugf("Recv ping, then Reply pong ...") //Recv ping, then Reply pong
			r.Reply(true, []byte("pong"))
		default:
			t.Debugf("Unknown request: %s", r.Type)
		}
	}
}

// * 业务请求处理函数-1
func (t *Tunnel) handleSSHChannels(chans <-chan ssh.NewChannel) {
	for ch := range chans {
		t.Debugf("handleSSHChannels ch:%v", &ch)
		go t.handleSSHChannel(ch)
	}
}

// * 业务请求处理函数-2
func (t *Tunnel) handleSSHChannel(ch ssh.NewChannel) {

	t.Debugf("handleSSHChannel ch:%v", &ch)
	if !t.Config.Outbound {
		t.Debugf("Denied outbound connection")
		ch.Reject(ssh.Prohibited, "Denied outbound connection")
		return
	}
	remote := string(ch.ExtraData())
	t.Debugf("handleSSHChannel remote:%v", remote)

	//extract protocol
	hostPort, proto := settings.L4Proto(remote)
	udp := proto == "udp"
	socks := hostPort == "socks"
	if socks && t.socksServer == nil {
		t.Debugf("Denied socks request, please enable socks")
		ch.Reject(ssh.Prohibited, "SOCKS5 is not enabled")
		return
	}
	sshChan, reqs, err := ch.Accept()
	if err != nil {
		t.Debugf("Failed to accept stream: %s", err)
		return
	}

	stream := io.ReadWriteCloser(sshChan)
	t.Debugf("accept sshChan:[%v]  stream:[%v]", sshChan, stream)

	//cnet.MeterRWC(t.Logger.Fork("sshchan"), sshChan)
	defer stream.Close()
	go ssh.DiscardRequests(reqs)
	l := t.Logger.Fork("conn#%d", t.connStats.New())

	//ready to handle
	t.connStats.Open()
	//TCP: Open [1/1] socks:[false] hostPort:[test05.testtest05.com:8080]
	//socks:  Open [1/2] socks:[true] hostPort:[socks]
	l.Debugf("Open %s socks:[%v] hostPort:[%v]", t.connStats.String(), socks, hostPort)
	if socks {
		err = t.handleSocks(stream)
	} else if udp {
		err = t.handleUDP(l, stream, hostPort)
	} else {
		//* Goto here
		err = t.handleTCP(l, stream, hostPort)
	}
	t.connStats.Close()
	errmsg := ""
	if err != nil && !strings.HasSuffix(err.Error(), "EOF") {
		errmsg = fmt.Sprintf(" (error %s)", err)
	}
	l.Debugf("Close %s%s", t.connStats.String(), errmsg)
}

func (t *Tunnel) handleSocks(src io.ReadWriteCloser) error {
	return t.socksServer.ServeConn(cnet.NewRWCConn(src))
}

func (t *Tunnel) handleTCP(l *cio.Logger, src io.ReadWriteCloser, hostPort string) error {

	l.Debugf("handleTCP: hostPort:[%v]", hostPort) //* handleTCP: hostPort:[test03.testtest03.com:8080]
	dst, err := net.Dial("tcp", hostPort)
	if err != nil {
		return err
	}
	s, r := cio.Pipe(src, dst)
	l.Debugf("handleTCP sent %s received %s", sizestr.ToString(s), sizestr.ToString(r))
	return nil
}
