package protocol

import (
	"errors"
	"fmt"
	"time"

	"github.com/dedis/kyber"
	"github.com/dedis/kyber/sign/cosi"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
)

func init() {
	GlobalRegisterDefaultProtocols()
}

// CoSiSubProtocolNode holds the different channels used to receive the different protocol messages.
type CoSiSubProtocolNode struct {
	*onet.TreeNodeInstance
	Publics        []kyber.Point
	Msg            []byte
	Data           []byte
	Timeout        time.Duration
	hasStopped     bool //used since Shutdown can be called multiple time
	verificationFn VerificationFn

	// protocol/subprotocol channels
	// these are used to communicate between the subprotocol and the main protocol
	subleaderNotResponding chan bool
	subCommitment          chan StructCommitment
	subResponse            chan StructResponse

	// internodes channels
	ChannelAnnouncement chan StructAnnouncement
	ChannelCommitment   chan StructCommitment
	ChannelChallenge    chan StructChallenge
	ChannelResponse     chan StructResponse
}

// NewDefaultSubProtocol is the default sub-protocol function used for registration
// with an always-true verification.
func NewDefaultSubProtocol(n *onet.TreeNodeInstance) (onet.ProtocolInstance, error) {
	vf := func(a, b []byte) bool { return true }
	return NewSubProtocol(n, vf)
}

// NewSubProtocol is used to define the subprotocol and to register
// the channels where the messages will be received.
func NewSubProtocol(n *onet.TreeNodeInstance, vf VerificationFn) (onet.ProtocolInstance, error) {

	c := &CoSiSubProtocolNode{
		TreeNodeInstance: n,
		hasStopped:       false,
		verificationFn:   vf,
	}

	if n.IsRoot() {
		c.subleaderNotResponding = make(chan bool)
		c.subCommitment = make(chan StructCommitment)
		c.subResponse = make(chan StructResponse)
	}

	for _, channel := range []interface{}{
		&c.ChannelAnnouncement,
		&c.ChannelCommitment,
		&c.ChannelChallenge,
		&c.ChannelResponse,
	} {
		err := c.RegisterChannel(channel)
		if err != nil {
			return nil, errors.New("couldn't register channel: " + err.Error())
		}
	}
	err := c.RegisterHandler(c.HandleStop)
	if err != nil {
		return nil, errors.New("couldn't register stop handler: " + err.Error())
	}
	return c, nil
}

// Shutdown stops the protocol
func (p *CoSiSubProtocolNode) Shutdown() error {
	if !p.hasStopped {
		close(p.ChannelAnnouncement)
		close(p.ChannelCommitment)
		close(p.ChannelChallenge)
		close(p.ChannelResponse)
		p.hasStopped = true
	}
	return nil
}

// Dispatch is the main method of the subprotocol, running on each node and handling the messages in order
func (p *CoSiSubProtocolNode) Dispatch() error {

	// ----- Announcement -----
	announcement, channelOpen := <-p.ChannelAnnouncement
	if !channelOpen {
		return nil
	}
	log.Lvl3(p.ServerIdentity().Address, "received announcement")
	p.Publics = announcement.Publics
	p.Timeout = announcement.Timeout
	p.Msg = announcement.Msg
	p.Data = announcement.Data
	suite, ok := p.Suite().(cosi.Suite)
	if !ok {
		return errors.New("not a cosi suite")
	}

	// start the verification in background if I'm not the root because
	// root does the verification in the main protocol
	verifyChan := make(chan bool, 1)
	if !p.IsRoot() {
		go func() {
			log.Lvl3(p.ServerIdentity().Address, "starting verification")
			verifyChan <- p.verificationFn(p.Msg, p.Data)
		}()
	}

	err := p.SendToChildren(&announcement.Announcement)
	if err != nil {
		return err
	}

	// ----- Commitment -----
	commitments := make([]StructCommitment, 0)
	if p.IsRoot() {
		select { // one commitment expected from super-protocol
		case commitment, channelOpen := <-p.ChannelCommitment:
			if !channelOpen {
				return nil
			}
			commitments = append(commitments, commitment)
		case <-time.After(p.Timeout):
			// the timeout here should be shorter than the main protocol timeout
			// because main protocol waits on the channel below
			p.subleaderNotResponding <- true
			return nil
		}
	} else {
		t := time.After(p.Timeout / 2)
	loop:
		for _ = range p.Children() {
			select {
			case commitment, channelOpen := <-p.ChannelCommitment:
				if !channelOpen {
					return nil
				}
				commitments = append(commitments, commitment)
			case <-t:
				break loop
			}
		}
	}

	committedChildren := make([]*onet.TreeNode, 0)
	for _, commitment := range commitments {
		if commitment.TreeNode.Parent != p.TreeNode() {
			return errors.New("received a Commitment from a non-Children node")
		}
		committedChildren = append(committedChildren, commitment.TreeNode)
	}
	log.Lvl3(p.ServerIdentity().Address, "finished receiving commitments, ", len(commitments), "commitment(s) received")

	var secret kyber.Scalar

	if p.IsRoot() {
		// send commitment to super-protocol
		if len(commitments) != 1 {
			return fmt.Errorf("root node in subprotocol should have received 1 commitment,"+
				"but received %d", len(commitments))
		}
		p.subCommitment <- commitments[0]
	} else {
		// do not commit if the verification does not succeed
		if !<-verifyChan {
			log.Lvl2(p.ServerIdentity().Address, "verification failed, terminating")
			return nil
		}

		// otherwise, compute personal commitment and send to parent
		var commitment kyber.Point
		var mask *cosi.Mask
		secret, commitment, mask, err = generateCommitmentAndAggregate(suite, p.TreeNodeInstance, p.Publics, commitments)
		if err != nil {
			return err
		}
		err = p.SendToParent(&Commitment{commitment, mask.Mask()})
		if err != nil {
			return err
		}
	}

	// ----- Challenge -----
	challenge, channelOpen := <-p.ChannelChallenge // from the leader
	if !channelOpen {
		return nil
	}

	log.Lvl3(p.ServerIdentity().Address, "received challenge")
	for _, TreeNode := range committedChildren {
		err = p.SendTo(TreeNode, &challenge.Challenge)
		if err != nil {
			return err
		}
	}

	// ----- Response -----
	if p.IsLeaf() {
		p.ChannelResponse <- StructResponse{}
	}
	responses := make([]StructResponse, 0)

	for _ = range committedChildren {
		response, channelOpen := <-p.ChannelResponse
		if !channelOpen {
			return nil
		}
		responses = append(responses, response)
	}
	log.Lvl3(p.ServerIdentity().Address, "received all", len(responses), "response(s)")

	if p.IsRoot() {
		// send response to super-protocol
		if len(responses) != 1 {
			return fmt.Errorf(
				"root node in subprotocol should have received 1 response, but received %v",
				len(commitments))
		}
		p.subResponse <- responses[0]
	} else {
		// generate own response and send to parent
		response, err := generateResponse(
			suite, p.TreeNodeInstance, responses, secret, challenge.Challenge.CoSiChallenge)
		if err != nil {
			return err
		}
		err = p.SendToParent(&Response{response})
		if err != nil {
			return err
		}
	}

	return nil
}

//HandleStop is called when a Stop message is send to this node.
// It broadcasts the message and stops the node
func (p *CoSiSubProtocolNode) HandleStop(stop StructStop) error {
	defer p.Done()
	if p.IsRoot() {
		p.Broadcast(&stop.Stop)
	}
	return nil
}

// Start is done only by root and starts the subprotocol
func (p *CoSiSubProtocolNode) Start() error {
	log.Lvl3("Starting subCoSi")
	if p.Msg == nil {
		return errors.New("subprotocol does not have a proposal msg")
	}
	// p.Data can be nil
	if p.Publics == nil || len(p.Publics) < 1 {
		return errors.New("subprotocol has invalid public keys")
	}
	if p.verificationFn == nil {
		return errors.New("subprotocol has an empty verification fn")
	}
	if p.Timeout < 10 {
		return errors.New("unrealistic timeout")
	}

	announcement := StructAnnouncement{
		p.TreeNode(),
		Announcement{p.Msg, p.Data, p.Publics, p.Timeout},
	}
	p.ChannelAnnouncement <- announcement
	return nil
}
