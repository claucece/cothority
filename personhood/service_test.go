package personhood

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/cothority/v3"
	"go.dedis.ch/cothority/v3/byzcoin"
	"go.dedis.ch/cothority/v3/byzcoin/contracts"
	"go.dedis.ch/cothority/v3/darc"
	pop "go.dedis.ch/cothority/v3/pop/service"
	"go.dedis.ch/cothority/v3/skipchain"
	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/sign/schnorr"
	"go.dedis.ch/kyber/v3/suites"
	"go.dedis.ch/kyber/v3/util/key"
	"go.dedis.ch/kyber/v3/util/random"
	"go.dedis.ch/onet/v3"
	"go.dedis.ch/onet/v3/log"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/protobuf"
)

var tSuite = suites.MustFind("Ed25519")

func TestMain(m *testing.M) {
	log.MainTest(m)
}

// Creates a party and links it, then verifies the account exists.
func TestService_LinkPoP(t *testing.T) {
	s := newS(t)
	defer s.Close()
	s.createParty(t, len(s.servers), 3)

	gpr, err := s.ols.GetProof(&byzcoin.GetProof{
		Version: byzcoin.CurrentVersion,
		Key:     s.serCoin.Slice(),
		ID:      s.olID,
	})
	require.Nil(t, err)
	require.True(t, gpr.Proof.InclusionProof.Match(s.serCoin.Slice()))
	_, _, cid, _, err := gpr.Proof.KeyValue()
	require.Nil(t, err)
	require.Equal(t, "coin", cid)
}

// Stores and loads a personhood data.
func TestService_SaveLoad(t *testing.T) {
	// Creates a party and links it, then verifies the account exists.
	s := newS(t)
	defer s.Close()
	s.createParty(t, len(s.servers), 3)

	s.phs[0].save()
	require.Nil(t, s.phs[0].tryLoad())
}

// Post a couple of questionnaires, get the list, and reply to some.
func TestService_Questionnaire(t *testing.T) {
	s := newS(t)
	defer s.Close()

	quests := []Questionnaire{
		{
			Title:     "qn1",
			Questions: []string{"q11", "q12", "q13"},
			Replies:   1,
			Balance:   10,
			Reward:    10,
			ID:        random.Bits(256, true, random.New()),
		},
		{
			Title:     "qn2",
			Questions: []string{"q11", "q12", "q13"},
			Replies:   2,
			Balance:   20,
			Reward:    10,
			ID:        random.Bits(256, true, random.New()),
		},
		{
			Title:     "qn3",
			Questions: []string{"q11", "q12", "q13"},
			Replies:   3,
			Balance:   30,
			Reward:    10,
			ID:        random.Bits(256, true, random.New()),
		},
	}

	// Register all questionnaires
	for _, q := range quests {
		_, err := s.phs[0].RegisterQuestionnaire(&RegisterQuestionnaire{
			Questionnaire: q,
		})
		require.Nil(t, err)
	}

	// Get a list of questionnaires
	for i := range quests {
		for j := 1; j < len(quests); j++ {
			lq, err := s.phs[0].ListQuestionnaires(&ListQuestionnaires{
				Start:  i,
				Number: j,
			})
			require.Nil(t, err)
			// Calculate the number of replies
			ll := j
			lm := len(quests) - i
			if lm < ll {
				ll = lm
			}
			require.Equal(t, ll, len(lq.Questionnaires))
			if len(lq.Questionnaires) > 0 {
				require.Equal(t, quests[len(quests)-i-1], lq.Questionnaires[0])
			}
		}
	}

	// Fill in some questionnaires
	aq := &AnswerQuestionnaire{
		QuestID: quests[0].ID,
		Replies: []int{-1},
		Account: byzcoin.InstanceID{},
	}
	_, err := s.phs[0].AnswerQuestionnaire(aq)
	require.NotNil(t, err)
	aq.Replies = []int{0, 1}
	_, err = s.phs[0].AnswerQuestionnaire(aq)
	require.NotNil(t, err)
	aq.Replies = []int{3}
	_, err = s.phs[0].AnswerQuestionnaire(aq)
	require.NotNil(t, err)
	aq.Replies = []int{0}
	_, err = s.phs[0].AnswerQuestionnaire(aq)
	require.Nil(t, err)
	// Now the first questionnaire should be out of credit
	_, err = s.phs[0].AnswerQuestionnaire(aq)
	require.NotNil(t, err)

	// Verify the list of questionnires is now down by one
	lqr, err := s.phs[0].ListQuestionnaires(&ListQuestionnaires{
		Start:  0,
		Number: len(quests),
	})
	require.Nil(t, err)
	require.Equal(t, len(quests)-1, len(lqr.Questionnaires))

	// Try to take the questionnaire by twice the same account
	// TODO: probably need a linkable ring signature here, because
	// later on the user might add an additional account.
	aq = &AnswerQuestionnaire{
		QuestID: quests[1].ID,
		Replies: []int{0, 1},
		Account: byzcoin.InstanceID{},
	}
	_, err = s.phs[0].AnswerQuestionnaire(aq)
	require.Nil(t, err)
	_, err = s.phs[0].AnswerQuestionnaire(aq)
	require.NotNil(t, err)

}

// Post a couple of questionnaires, get the list, and reply to some.
func TestService_Messages(t *testing.T) {
	s := newS(t)
	defer s.Close()
	s.createParty(t, len(s.servers), 3)

	msgs := []Message{
		{
			Subject: "test1",
			Date:    0,
			Text:    "This is the 1st test message",
			Author:  byzcoin.InstanceID{},
			Balance: 10,
			Reward:  10,
			ID:      random.Bits(256, true, random.New()),
		},
		{
			Subject: "test2",
			Date:    0,
			Text:    "This is the 2nd test message",
			Author:  byzcoin.InstanceID{},
			Balance: 20,
			Reward:  10,
			ID:      random.Bits(256, true, random.New()),
		},
	}

	// Register messages
	for _, msg := range msgs {
		log.Lvl1("Registering message", msg.Subject)
		s.coinTransfer(t, s.attCoin[0], s.serCoin, msg.Balance, s.attDarc[0], s.attSig[0])
		_, err := s.phs[0].SendMessage(&SendMessage{msg})
		require.Nil(t, err)
	}

	// List messages
	log.Lvl1("Listing messages")
	for i := range msgs {
		for j := 1; j < len(msgs); j++ {
			lmr, err := s.phs[0].ListMessages(&ListMessages{
				Start:  i,
				Number: j,
			})
			require.Nil(t, err)
			// Calculate the number of replies
			ll := j
			lm := len(msgs) - i
			if lm < ll {
				ll = lm
			}
			require.Equal(t, ll, len(lmr.Subjects))
			if len(lmr.Subjects) > 0 {
				require.Equal(t, msgs[len(msgs)-i-1].Subject, lmr.Subjects[0])
			}
		}
	}

	// Read a message and get reward
	log.Lvl1("Read a message and get reward")
	ciBefore := s.coinGet(t, s.attCoin[1])
	rm := &ReadMessage{
		MsgID:    msgs[1].ID,
		Reader:   s.attCoin[1],
		PartyIID: s.popI.Slice(),
	}
	rmr, err := s.phs[0].ReadMessage(rm)
	require.Nil(t, err)
	require.EqualValues(t, msgs[1].ID, rmr.Message.ID)
	require.Equal(t, msgs[1].Balance-msgs[1].Reward, rmr.Message.Balance)
	// Don't get reward for double-read
	rmr, err = s.phs[0].ReadMessage(rm)
	require.Nil(t, err)
	require.Equal(t, msgs[1].Balance-msgs[1].Reward, rmr.Message.Balance)

	// Check reward on account.
	log.Lvl1("Check reward")
	ciAfter := s.coinGet(t, s.attCoin[1])
	require.Equal(t, msgs[1].Reward, ciAfter.Value-ciBefore.Value)

	// Have other reader get message and put its balance to 0, thus
	// making it disappear from the list of messages.
	rm.Reader = s.attCoin[2]
	rmr, err = s.phs[0].ReadMessage(rm)
	require.Nil(t, err)
	require.Equal(t, uint64(0), rmr.Message.Balance)

	lmr, err := s.phs[0].ListMessages(&ListMessages{
		Start:  0,
		Number: len(msgs),
	})
	require.Nil(t, err)
	require.Equal(t, len(msgs)-1, len(lmr.MsgIDs))

	// Top up message
	_, err = s.phs[0].TopupMessage(&TopupMessage{
		MsgID:  msgs[1].ID,
		Amount: msgs[1].Reward,
	})

	// Should be here again
	lmr, err = s.phs[0].ListMessages(&ListMessages{
		Start:  0,
		Number: len(msgs),
	})
	require.Nil(t, err)
	require.Equal(t, len(msgs), len(lmr.MsgIDs))
}

type sStruct struct {
	local     *onet.LocalTest
	servers   []*onet.Server
	roster    *onet.Roster
	services  []onet.Service
	phs       []*Service
	pops      []*pop.Service
	party     pop.FinalStatement
	orgs      []*key.Pair
	attendees []*key.Pair
	attCoin   []byzcoin.InstanceID
	attDarc   []*darc.Darc
	attSig    []darc.Signer
	service   *key.Pair
	serDarc   *darc.Darc
	serCoin   byzcoin.InstanceID
	serSig    darc.Signer
	ols       *byzcoin.Service
	olID      skipchain.SkipBlockID
	signer    darc.Signer
	gMsg      *byzcoin.CreateGenesisBlock
	popI      byzcoin.InstanceID
}

func newS(t *testing.T) (s *sStruct) {
	s = &sStruct{}
	s.local = onet.NewTCPTest(tSuite)
	s.servers, s.roster, _ = s.local.GenTree(5, true)

	s.services = s.local.GetServices(s.servers, templateID)
	for _, p := range s.services {
		s.phs = append(s.phs, p.(*Service))
	}
	popsS := s.local.GetServices(s.servers, onet.ServiceFactory.ServiceID(pop.Name))
	for _, p := range popsS {
		s.pops = append(s.pops, p.(*pop.Service))
	}

	// Create the ledger
	s.ols = s.local.Services[s.roster.List[0].ID][onet.ServiceFactory.ServiceID(byzcoin.ServiceName)].(*byzcoin.Service)
	s.signer = darc.NewSignerEd25519(nil, nil)
	var err error
	s.gMsg, err = byzcoin.DefaultGenesisMsg(byzcoin.CurrentVersion, s.roster,
		[]string{"spawn:dummy", "spawn:" + pop.ContractPopParty, "invoke:" + pop.ContractPopParty + ".Finalize"}, s.signer.Identity())
	require.Nil(t, err)
	s.gMsg.BlockInterval = 500 * time.Millisecond

	resp, err := s.ols.CreateGenesisBlock(s.gMsg)
	require.Nil(t, err)
	s.olID = resp.Skipblock.SkipChainID()
	return
}

func (s *sStruct) Close() {
	s.local.CloseAll()
}

// Create a party with orgs organizers and attendees. It will store the party
// in the ledger and finalize it.
func (s *sStruct) createParty(t *testing.T, orgs, attendees int) {
	if orgs > len(s.pops) {
		t.Fatal("cannot have more organizers than conodes")
	}
	for i := 0; i < orgs; i++ {
		org := key.NewKeyPair(tSuite)
		s.orgs = append(s.orgs, org)
		s.pops[i].StoreLink(org.Public)
	}
	for i := 0; i < attendees; i++ {
		s.attendees = append(s.attendees, key.NewKeyPair(tSuite))
	}
	s.party = pop.FinalStatement{
		Desc: &pop.PopDesc{
			Name:     "test-party",
			DateTime: "2018-08-28 08:08",
			Location: "BC208",
			Roster:   s.roster,
		},
	}

	// Publish the party
	log.Lvl2("Publishing the party to the pop-service")
	var atts []kyber.Point
	for _, att := range s.attendees {
		atts = append(atts, att.Public)
	}
	ph := s.party.Desc.Hash()
	for i, org := range s.orgs {
		sg, err := schnorr.Sign(tSuite, org.Private, ph)
		require.Nil(t, err)
		_, err = s.pops[i].StoreConfig(&pop.StoreConfig{
			Desc:      s.party.Desc,
			Signature: sg,
		})
		require.Nil(t, err)
	}

	// Store the party in the ledger
	s.createPoPSpawn(t)

	// Finalise the party
	log.Lvl2("Finalizing the party in the pop-service")
	for i, org := range s.orgs {
		req := &pop.FinalizeRequest{
			DescID:    ph,
			Attendees: atts,
		}
		reqH, err := req.Hash()
		require.Nil(t, err)
		req.Signature, err = schnorr.Sign(tSuite, org.Private, reqH)
		require.Nil(t, err)
		fr, err := s.pops[i].FinalizeRequest(req)
		if err != nil && i == len(s.orgs)-1 {
			t.Fatal("Shouldn't get error in last finalization-request: " + err.Error())
		} else if err == nil {
			s.party = *fr.Final
		}
	}

	// Store the finalized party in the ledger
	s.invokePoPFinalize(t)

	_, err := s.phs[0].LinkPoP(&LinkPoP{
		Party: Party{
			ByzCoinID:      s.olID,
			InstanceID:     s.popI,
			FinalStatement: s.party,
			Darc:           *s.serDarc,
			Signer:         s.serSig,
		},
	})
	require.Nil(t, err)
}

func (s *sStruct) createPoPSpawn(t *testing.T) {
	log.Lvl2("Publishing the party to the ledger")

	signerCtrs, err := s.ols.GetSignerCounters(&byzcoin.GetSignerCounters{
		SignerIDs:   []string{s.signer.Identity().String()},
		SkipchainID: s.olID,
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(signerCtrs.Counters))

	fsBuf, err := protobuf.Encode(&s.party)
	require.Nil(t, err)
	dID := s.gMsg.GenesisDarc.GetBaseID()
	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{byzcoin.Instruction{
			InstanceID: byzcoin.NewInstanceID(dID),
			Spawn: &byzcoin.Spawn{
				ContractID: pop.ContractPopParty,
				Args: byzcoin.Arguments{{
					Name:  "FinalStatement",
					Value: fsBuf,
				}},
			},
			SignerCounter: []uint64{signerCtrs.Counters[0] + 1},
		}},
	}
	err = ctx.FillSignersAndSignWith(s.signer)
	require.Nil(t, err)
	_, err = s.ols.AddTransaction(&byzcoin.AddTxRequest{
		Version:       byzcoin.CurrentVersion,
		SkipchainID:   s.olID,
		Transaction:   ctx,
		InclusionWait: 10,
	})
	require.Nil(t, err)
	s.popI = ctx.Instructions[0].DeriveID("")
}

func (s *sStruct) invokePoPFinalize(t *testing.T) {
	log.Lvl2("finalizing the party in the ledger")

	signerCtrs, err := s.ols.GetSignerCounters(&byzcoin.GetSignerCounters{
		SignerIDs:   []string{s.signer.Identity().String()},
		SkipchainID: s.olID,
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(signerCtrs.Counters))

	fsBuf, err := protobuf.Encode(&s.party)
	require.Nil(t, err)
	s.service = key.NewKeyPair(tSuite)
	sBuf, err := s.service.Public.MarshalBinary()
	require.Nil(t, err)
	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{byzcoin.Instruction{
			InstanceID: s.popI,
			Invoke: &byzcoin.Invoke{
				ContractID: pop.ContractPopParty,
				Command:    "Finalize",
				Args: byzcoin.Arguments{
					{
						Name:  "FinalStatement",
						Value: fsBuf,
					},
					{
						Name:  "Service",
						Value: sBuf,
					},
				},
			},
			SignerCounter: []uint64{signerCtrs.Counters[0] + 1},
		}},
	}
	dID := s.gMsg.GenesisDarc.GetBaseID()
	err = ctx.FillSignersAndSignWith(s.signer)
	require.Nil(t, err)
	_, err = s.ols.AddTransaction(&byzcoin.AddTxRequest{
		Version:       byzcoin.CurrentVersion,
		SkipchainID:   s.olID,
		Transaction:   ctx,
		InclusionWait: 10,
	})
	require.Nil(t, err)
	serCoinID := sha256.New()
	serCoinID.Write(ctx.Instructions[0].InstanceID.Slice())
	serCoinID.Write(sBuf)
	s.serCoin = byzcoin.NewInstanceID(serCoinID.Sum(nil))
	gpr, err := s.ols.GetProof(&byzcoin.GetProof{
		Version: byzcoin.CurrentVersion,
		Key:     s.serCoin.Slice(),
		ID:      s.olID,
	})
	require.Nil(t, err)
	require.True(t, gpr.Proof.InclusionProof.Match(s.serCoin.Slice()))
	_, _, _, dID, err = gpr.Proof.KeyValue()
	require.Nil(t, err)
	gpr, err = s.ols.GetProof(&byzcoin.GetProof{
		Version: byzcoin.CurrentVersion,
		Key:     dID,
		ID:      s.olID,
	})
	require.Nil(t, err)
	require.True(t, gpr.Proof.InclusionProof.Match(dID))
	_, v0, _, _, err := gpr.Proof.KeyValue()
	require.Nil(t, err)
	s.serDarc, err = darc.NewFromProtobuf(v0)
	require.Nil(t, err)
	s.serSig = darc.NewSignerEd25519(s.service.Public, s.service.Private)

	// Get attendees coin instances
	for i, att := range s.attendees {
		inst := sha256.New()
		inst.Write(s.popI.Slice())
		buf, err := att.Public.MarshalBinary()
		require.Nil(t, err)
		inst.Write(buf)
		s.attCoin = append(s.attCoin, byzcoin.NewInstanceID(inst.Sum(nil)))
		s.attSig = append(s.attSig, darc.NewSignerEd25519(att.Public, att.Private))
		gpr, err = s.ols.GetProof(&byzcoin.GetProof{
			Version: byzcoin.CurrentVersion,
			Key:     s.attCoin[i].Slice(),
			ID:      s.olID,
		})
		require.Nil(t, err)
		require.True(t, gpr.Proof.InclusionProof.Match(s.attCoin[i].Slice()))
		_, _, _, dID, err := gpr.Proof.KeyValue()
		require.Nil(t, err)
		gpr, err = s.ols.GetProof(&byzcoin.GetProof{
			Version: byzcoin.CurrentVersion,
			Key:     dID,
			ID:      s.olID,
		})
		require.Nil(t, err)
		require.True(t, gpr.Proof.InclusionProof.Match(dID))
		_, v0, _, _, err = gpr.Proof.KeyValue()
		require.Nil(t, err)
		var d darc.Darc
		err = protobuf.DecodeWithConstructors(v0, &d, network.DefaultConstructors(cothority.Suite))
		require.Nil(t, err)
		s.attDarc = append(s.attDarc, &d)
	}
}

func (s *sStruct) coinGet(t *testing.T, inst byzcoin.InstanceID) (ci byzcoin.Coin) {
	gpr, err := s.ols.GetProof(&byzcoin.GetProof{
		Version: byzcoin.CurrentVersion,
		Key:     inst.Slice(),
		ID:      s.olID,
	})
	require.Nil(t, err)
	require.True(t, gpr.Proof.InclusionProof.Match(inst.Slice()))
	_, v0, cid, _, err := gpr.Proof.KeyValue()
	require.Nil(t, err)
	require.Equal(t, contracts.ContractCoinID, cid)
	err = protobuf.Decode(v0, &ci)
	require.Nil(t, err)
	return
}

func (s *sStruct) coinTransfer(t *testing.T, from, to byzcoin.InstanceID, coins uint64, d *darc.Darc, sig darc.Signer) {
	signerCtrs, err := s.ols.GetSignerCounters(&byzcoin.GetSignerCounters{
		SignerIDs:   []string{sig.Identity().String()},
		SkipchainID: s.olID,
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(signerCtrs.Counters))

	var cBuf = make([]byte, 8)
	binary.LittleEndian.PutUint64(cBuf, coins)
	ctx := byzcoin.ClientTransaction{
		Instructions: []byzcoin.Instruction{{
			InstanceID: from,
			Invoke: &byzcoin.Invoke{
				ContractID: contracts.ContractCoinID,
				Command:    "transfer",
				Args: []byzcoin.Argument{{
					Name:  "coins",
					Value: cBuf,
				},
					{
						Name:  "destination",
						Value: to.Slice(),
					}},
			},
			SignerCounter: []uint64{signerCtrs.Counters[0] + 1},
		}},
	}
	require.Nil(t, ctx.FillSignersAndSignWith(sig))
	_, err = s.ols.AddTransaction(&byzcoin.AddTxRequest{
		Version:       byzcoin.CurrentVersion,
		SkipchainID:   s.olID,
		Transaction:   ctx,
		InclusionWait: 10,
	})
	require.Nil(t, err)
}
