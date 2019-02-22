package personhood

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/cothority/v3/byzcoin"
	"go.dedis.ch/cothority/v3/byzcoin/contracts"
	"go.dedis.ch/cothority/v3/darc"
	"go.dedis.ch/cothority/v3/skipchain"
	"go.dedis.ch/kyber/v3/suites"
	"go.dedis.ch/kyber/v3/util/key"
	"go.dedis.ch/kyber/v3/util/random"
	"go.dedis.ch/onet/v3"
	"go.dedis.ch/onet/v3/log"
	"go.dedis.ch/protobuf"
)

var tSuite = suites.MustFind("Ed25519")

func TestMain(m *testing.M) {
	log.MainTest(m)
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
	local       *onet.LocalTest
	cl          *byzcoin.Client
	servers     []*onet.Server
	roster      *onet.Roster
	services    []onet.Service
	phs         []*Service
	genesisDarc *darc.Darc
	party       FinalStatement
	orgs        []*key.Pair
	attendees   []*key.Pair
	attCoin     []byzcoin.InstanceID
	attDarc     []*darc.Darc
	attSig      []darc.Signer
	service     *key.Pair
	serDarc     *darc.Darc
	serCoin     byzcoin.InstanceID
	serSig      darc.Signer
	ols         *byzcoin.Service
	olID        skipchain.SkipBlockID
	signer      darc.Signer
	gMsg        *byzcoin.CreateGenesisBlock
	popI        byzcoin.InstanceID
}

func newS(t *testing.T) (s *sStruct) {
	s = &sStruct{}
	s.local = onet.NewTCPTest(tSuite)
	s.servers, s.roster, _ = s.local.GenTree(5, true)

	s.services = s.local.GetServices(s.servers, templateID)
	for _, p := range s.services {
		s.phs = append(s.phs, p.(*Service))
	}

	// Create the ledger
	s.ols = s.local.Services[s.roster.List[0].ID][onet.ServiceFactory.ServiceID(byzcoin.ServiceName)].(*byzcoin.Service)
	s.signer = darc.NewSignerEd25519(nil, nil)
	var err error
	s.gMsg, err = byzcoin.DefaultGenesisMsg(byzcoin.CurrentVersion, s.roster,
		[]string{"spawn:dummy", "spawn:popParty", "invoke:popParty.finalize", "invoke:popParty.barrier",
			"invoke:popParty.mine",
			"spawn:ropasci", "invoke:ropasci.second", "invoke:ropasci.confirm"}, s.signer.Identity())
	require.Nil(t, err)
	s.gMsg.BlockInterval = 500 * time.Millisecond

	resp, err := s.ols.CreateGenesisBlock(s.gMsg)
	s.genesisDarc = &s.gMsg.GenesisDarc
	require.Nil(t, err)
	s.olID = resp.Skipblock.SkipChainID()
	s.cl = byzcoin.NewClient(s.olID, *s.roster)
	return
}

func (s *sStruct) Close() {
	s.local.CloseAll()
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
