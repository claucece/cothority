package personhood

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"go.dedis.ch/kyber/v3/util/key"

	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/group/edwards25519"
	"go.dedis.ch/kyber/v3/sign/anon"
	"go.dedis.ch/kyber/v3/xof/blake2xs"
	"go.dedis.ch/onet/v3/log"

	"go.dedis.ch/cothority/v3"
	"go.dedis.ch/cothority/v3/byzcoin"
	"go.dedis.ch/cothority/v3/byzcoin/contracts"
	"go.dedis.ch/cothority/v3/darc"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/protobuf"
)

// ContractPopPartyID represents a pop-party that can be in one of three states:
//   1 - configuration
//   2 - scanning
//   3 - finalized
var ContractPopPartyID = "popParty"

// ContractPopParty embeds the BasicContract to be able to verify the calling darc is respected.
type ContractPopParty struct {
	byzcoin.BasicContract
	PopPartyStruct
}

// ContractPopPartyFromBytes returns the ContractPopPary structure given a slice of bytes.
func ContractPopPartyFromBytes(in []byte) (byzcoin.Contract, error) {
	c := &ContractPopParty{}
	err := protobuf.DecodeWithConstructors(in, &c.PopPartyStruct, network.DefaultConstructors(cothority.Suite))
	if err != nil {
		return nil, errors.New("couldn't unmarshal existing PopPartyStruct: " + err.Error())
	}
	return c, nil
}

// VerifyInstruction overrides the basic VerifyInstruction in case of a "mine" command, because this command
// is not protected by a darc, but by a linkable ring signature.
func (c ContractPopParty) VerifyInstruction(rst byzcoin.ReadOnlyStateTrie, inst byzcoin.Instruction, ctxHash []byte) error {
	if inst.GetType() == byzcoin.InvokeType && inst.Invoke.Command == "mine" {
		log.Lvl2("not verifying darc for mining")
		return nil
	}
	return c.BasicContract.VerifyInstruction(rst, inst, ctxHash)
}

// Spawn creates a new pop party contract. The following arguments are needed:
//  - description holds a protobuf encoded 'Description'
//  - darcID holds the id of the darc responsible for the pop party
//  - miningReward defines how much the 'mine' command will put into a coin-account
func (c ContractPopParty) Spawn(rst byzcoin.ReadOnlyStateTrie, inst byzcoin.Instruction, coins []byzcoin.Coin) (scs []byzcoin.StateChange, cout []byzcoin.Coin, err error) {
	cout = coins

	descBuf := inst.Spawn.Args.Search("description")
	if descBuf == nil {
		return nil, nil, errors.New("need description argument")
	}
	darcID := inst.Spawn.Args.Search("darcID")
	if darcID == nil {
		return nil, nil, errors.New("no darcID argument")
	}
	c.State = 1

	err = protobuf.DecodeWithConstructors(descBuf, &c.Description, network.DefaultConstructors(cothority.Suite))
	if err != nil {
		return nil, nil, errors.New("couldn't unmarshal the description: " + err.Error())
	}

	value, _, _, _, err := rst.GetValues(darcID)
	if err != nil {
		return nil, nil, errors.New("couldn't get darc in charge: " + err.Error())
	}
	d, err := darc.NewFromProtobuf(value)
	if err != nil {
		return nil, nil, errors.New("couldn't get darc: " + err.Error())
	}
	expr := d.Rules.Get("invoke:finalize")
	c.Organizers = len(strings.Split(string(expr), "|"))

	miningRewardBuf := inst.Spawn.Args.Search("miningReward")
	if miningRewardBuf == nil {
		return nil, nil, errors.New("no miningReward argument")
	}
	c.MiningReward = binary.LittleEndian.Uint64(miningRewardBuf)

	ppiBuf, err := protobuf.Encode(&c.PopPartyStruct)
	if err != nil {
		return nil, nil, errors.New("couldn't marshal PopPartyStruct: " + err.Error())
	}

	scs = byzcoin.StateChanges{
		byzcoin.NewStateChange(byzcoin.Create, inst.DeriveID(""), ContractPopPartyID, ppiBuf, darcID),
	}
	return
}

type suiteBlake2s struct {
	edwards25519.SuiteEd25519
}

// XOF uses the blake2xs, as the blake2xb is not readily available in javascript.
func (sb suiteBlake2s) XOF(key []byte) kyber.XOF {
	return blake2xs.New(key)
}

// Invoke uses the following commands:
//  - barrier to activate the pop-party
//  - finalize to store the attendees. If all organizers finalize using the same list of attendees,
//    the party is finalized
//  - addParty to add a new party to the list - not supported yet
//  - mine to collect the reward. 'lrs' must hold a correct, unique linkable ring signature. If
//    'coinIID' is set, this coin will be filled. Else 'newDarc' will be used to create a darc,
//    derive a coin, and fill this coin.
func (c *ContractPopParty) Invoke(rst byzcoin.ReadOnlyStateTrie, inst byzcoin.Instruction, coins []byzcoin.Coin) (scs []byzcoin.StateChange, cout []byzcoin.Coin, err error) {
	cout = coins

	var darcID darc.ID
	_, _, _, darcID, err = rst.GetValues(inst.InstanceID.Slice())
	if err != nil {
		return nil, nil, errors.New("couldn't get instance data: " + err.Error())
	}

	switch inst.Invoke.Command {
	case "barrier":
		if c.State != 1 {
			return nil, nil, fmt.Errorf("can only start barrier point when in configuration mode")
		}
		c.State = 2

	case "finalize":
		if c.State != 2 {
			return nil, nil, fmt.Errorf("can only finalize when barrier point is passed")
		}

		attBuf := inst.Invoke.Args.Search("attendees")
		if attBuf == nil {
			return nil, nil, errors.New("missing argument: attendees")
		}
		var atts Attendees
		err = protobuf.DecodeWithConstructors(attBuf, &atts, network.DefaultConstructors(cothority.Suite))
		log.Lvl2("Adding attendees:", atts.Keys)

		alreadySigned := false
		orgSigner := inst.SignerIdentities[0].String()
		for _, f := range c.Finalizations {
			if f == orgSigner {
				alreadySigned = true
				log.Lvl2("this organizer already sent a finalization - resetting list of attendees")
				break
			}
		}

		if len(c.Finalizations) == 0 || alreadySigned {
			// Store first proposition of list of attendees or reset if the same
			// organizer submits again
			c.Attendees = atts
			c.Finalizations = []string{orgSigner}
			log.Lvl2("resetting list of attendees")
		} else {
			// Check if it is the same set of attendees or not
			same := len(c.Attendees.Keys) == len(atts.Keys)
			if same {
				for i, att := range c.Attendees.Keys {
					if !att.Equal(atts.Keys[i]) {
						same = false
					}
				}
			}
			if same {
				log.Lvl2("one more finalization")
				c.Finalizations = append(c.Finalizations, orgSigner)
			} else {
				log.Lvl2("not the same list of attendees - resetting")
				c.Attendees = atts
				c.Finalizations = []string{orgSigner}
			}
		}
		if len(c.Finalizations) == c.Organizers {
			log.Lvlf2("Successfully finalized party %s / %x", c.Description.Name, inst.InstanceID[:])
			c.State = 3
		}

	case "addParty":
		if c.State != 3 {
			return nil, nil, errors.New("cannot add party when party is not finalized")
		}
		return nil, nil, errors.New("not yet implemented")

	case "mine":
		if c.State != 3 {
			return nil, nil, errors.New("cannot mine when party is not finalized")
		}
		lrs := inst.Invoke.Args.Search("lrs")
		if lrs == nil {
			return nil, nil, errors.New("need lrs argument")
		}
		tag, err := anon.Verify(&suiteBlake2s{}, []byte("mine"), c.Attendees.Keys, inst.InstanceID[:], lrs)
		if err != nil {
			return nil, nil, errors.New("error while verifying signature: " + err.Error())
		}
		for _, t := range c.Miners {
			if bytes.Compare(t.Tag, tag) == 0 {
				return nil, nil, errors.New("this attendee already mined")
			}
		}
		c.Miners = append(c.Miners, LRSTag{Tag: tag})

		var coin byzcoin.Coin
		var coinDarc darc.ID
		coinAction := byzcoin.Update
		coinIID := inst.Invoke.Args.Search("coinIID")
		if coinIID == nil {
			newDarcBuf := inst.Invoke.Args.Search("newDarc")
			if newDarcBuf == nil {
				return nil, nil, errors.New("need either coinIID or newDarc argument")
			}
			newDarc, err := darc.NewFromProtobuf(newDarcBuf)
			if err != nil {
				return nil, nil, errors.New("couldn't unmarshal darc: " + err.Error())
			}
			// Creating new darc for new user
			log.Lvlf2("Creating new darc %x for user", newDarc.GetBaseID())
			scs = append(scs, byzcoin.NewStateChange(byzcoin.Create,
				byzcoin.NewInstanceID(newDarc.GetBaseID()), byzcoin.ContractDarcID,
				newDarcBuf, darcID))
			coinAction = byzcoin.Create
			h := sha256.New()
			h.Write([]byte("coin"))
			h.Write(newDarc.GetBaseID())
			coinIID = h.Sum(nil)
			coinDarc = newDarc.GetBaseID()
			log.Lvlf2("Creating new coin %x for user", coinIID)
			coin.Name = byzcoin.NewInstanceID([]byte("SpawnerCoin"))
		} else {
			var cid string
			var coinBuf []byte
			coinBuf, _, cid, coinDarc, err = rst.GetValues(coinIID)
			if cid != contracts.ContractCoinID {
				return nil, nil, errors.New("coinIID is not a coin contract")
			}
			err = protobuf.Decode(coinBuf, &coin)
			if err != nil {
				return nil, nil, errors.New("couldn't unmarshal coin: " + err.Error())
			}
		}
		err = coin.SafeAdd(c.MiningReward)
		if err != nil {
			return nil, nil, errors.New("couldn't add mining reward: " + err.Error())
		}
		coinBuf, err := protobuf.Encode(&coin)
		if err != nil {
			return nil, nil, errors.New("couldn't encode coin: " + err.Error())
		}
		scs = append(scs, byzcoin.NewStateChange(coinAction,
			byzcoin.NewInstanceID(coinIID),
			contracts.ContractCoinID, coinBuf, coinDarc))

	default:
		return nil, nil, errors.New("unknown command: " + inst.Invoke.Command)
	}

	// Storing new version of PopPartyStruct
	ppiBuf, err := protobuf.Encode(&c.PopPartyStruct)
	if err != nil {
		return nil, nil, errors.New("couldn't marshal PopPartyStruct: " + err.Error())
	}

	// Update existing party structure
	scs = append(scs, byzcoin.NewStateChange(byzcoin.Update, inst.InstanceID, ContractPopPartyID, ppiBuf, darcID))

	return scs, coins, nil
}

// PopPartySpawn returns the instanceID of the newly created pop-party, or an error if it
// wasn't successful.
func PopPartySpawn(cl *byzcoin.Client, fs FinalStatement, dID darc.ID, reward uint64, signers ...darc.Signer) (popIID byzcoin.InstanceID, err error) {
	var sigStrs []string
	for _, sig := range signers {
		sigStrs = append(sigStrs, sig.Identity().String())
	}
	signerCtrs, err := cl.GetSignerCounters(sigStrs...)
	if err != nil {
		return
	}

	fsBuf, err := protobuf.Encode(&fs)
	if err != nil {
		return
	}
	mr := make([]byte, 8)
	binary.LittleEndian.PutUint64(mr, reward)
	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{byzcoin.Instruction{
			InstanceID: byzcoin.NewInstanceID(dID),
			Spawn: &byzcoin.Spawn{
				ContractID: ContractPopPartyID,
				Args: byzcoin.Arguments{{
					Name:  "description",
					Value: fsBuf,
				}, {
					Name:  "darcID",
					Value: dID,
				}, {
					Name:  "miningReward",
					Value: mr,
				}},
			},
			SignerCounter: []uint64{signerCtrs.Counters[0] + 1},
		}},
	}
	err = ctx.FillSignersAndSignWith(signers...)
	if err != nil {
		return
	}
	_, err = cl.AddTransactionAndWait(ctx, 5)
	if err != nil {
		return
	}
	return ctx.Instructions[0].DeriveID(""), nil
}

// PopPartyBarrier activates the barrier in the pop-party.
func PopPartyBarrier(cl *byzcoin.Client, popIID byzcoin.InstanceID, signers ...darc.Signer) (err error) {
	var sigStrs []string
	for _, sig := range signers {
		sigStrs = append(sigStrs, sig.Identity().String())
	}
	signerCtrs, err := cl.GetSignerCounters(sigStrs...)
	if err != nil {
		return
	}

	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{byzcoin.Instruction{
			InstanceID: popIID,
			Invoke: &byzcoin.Invoke{
				ContractID: ContractPopPartyID,
				Command:    "barrier",
			},
			SignerCounter: []uint64{signerCtrs.Counters[0] + 1},
		}},
	}
	err = ctx.FillSignersAndSignWith(signers...)
	if err != nil {
		return
	}
	_, err = cl.AddTransactionAndWait(ctx, 5)
	return
}

// PopPartyFinalize sends the list of attendees to the party for finalization.
func PopPartyFinalize(cl *byzcoin.Client, popIID byzcoin.InstanceID, atts Attendees, signers ...darc.Signer) (err error) {
	var sigStrs []string
	for _, sig := range signers {
		sigStrs = append(sigStrs, sig.Identity().String())
	}
	signerCtrs, err := cl.GetSignerCounters(sigStrs...)
	if err != nil {
		return
	}

	attBuff, err := protobuf.Encode(&atts)
	if err != nil {
		return
	}
	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{byzcoin.Instruction{
			InstanceID: popIID,
			Invoke: &byzcoin.Invoke{
				ContractID: ContractPopPartyID,
				Command:    "finalize",
				Args: byzcoin.Arguments{
					{
						Name:  "attendees",
						Value: attBuff,
					},
				},
			},
			SignerCounter: []uint64{signerCtrs.Counters[0] + 1},
		}},
	}
	err = ctx.FillSignersAndSignWith(signers...)
	if err != nil {
		return
	}
	_, err = cl.AddTransactionAndWait(ctx, 5)
	return
}

// PopPartyMine collects the reward. If 'atts' is nil, the party will be fetched from
// byzcoin. One of 'coinIID' or 'd' must be set to either mine the coin, or spawn
// a new darc and coin.
func PopPartyMine(cl *byzcoin.Client, popIID byzcoin.InstanceID, kp key.Pair,
	atts *Attendees, coinIID *byzcoin.InstanceID, d *darc.Darc) (err error) {
	if atts == nil {
		popProof, err := cl.GetProof(popIID.Slice())
		if err != nil {
			return err
		}
		_, value, cID, _, err := popProof.Proof.KeyValue()
		if err != nil {
			return err
		}
		if cID != ContractPopPartyID {
			return errors.New("given popIID is not of contract-type PopParty")
		}
		var pop PopPartyStruct
		err = protobuf.DecodeWithConstructors(value, &pop, network.DefaultConstructors(cothority.Suite))
		if err != nil {
			return err
		}
		atts = &pop.Attendees
	}
	var mine = -1
	for i, p := range atts.Keys {
		if p.Equal(kp.Public) {
			mine = i
			break
		}
	}
	if mine == -1 {
		return errors.New("didn't find public key of keypair in attendees")
	}

	lrs := anon.Sign(&suiteBlake2s{}, []byte("mine"), atts.Keys, popIID[:], mine, kp.Private)
	args := byzcoin.Arguments{{
		Name:  "lrs",
		Value: lrs,
	}}
	if coinIID == nil {
		darcBuf, err := d.ToProto()
		if err != nil {
			return err
		}

		args = append(args, byzcoin.Argument{
			Name:  "newDarc",
			Value: darcBuf,
		})
	} else {
		args = append(args, byzcoin.Argument{
			Name:  "coinID",
			Value: coinIID.Slice(),
		})
	}

	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{byzcoin.Instruction{
			InstanceID: popIID,
			Invoke: &byzcoin.Invoke{
				ContractID: ContractPopPartyID,
				Command:    "mine",
				Args:       args,
			},
		}},
	}
	_, err = cl.AddTransactionAndWait(ctx, 5)
	return
}

// PopPartyMineDarcToCoin calculates the coin given a darc and returns the coin instance.
func PopPartyMineDarcToCoin(cl *byzcoin.Client, d *darc.Darc) (coinIID byzcoin.InstanceID, coin byzcoin.Coin, err error) {
	h := sha256.New()
	h.Write([]byte("coin"))
	h.Write(d.GetBaseID())
	coinIID = byzcoin.NewInstanceID(h.Sum(nil))

	var proof *byzcoin.GetProofResponse
	proof, err = cl.GetProof(coinIID.Slice())
	if err != nil {
		return
	}
	var value []byte
	_, value, _, _, err = proof.Proof.KeyValue()
	if err != nil {
		return
	}
	err = protobuf.Decode(value, &coin)
	return
}
