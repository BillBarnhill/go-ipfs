package commands

import (
	"context"
	"fmt"
	"io"
	"os"

	core "github.com/ipfs/go-ipfs/core"
	coreunix "github.com/ipfs/go-ipfs/core/coreunix"

	"gx/ipfs/QmSRaAPPNxyhnXeDa5NXtZ2CWBYJ6BRWNQp6gKxhPcoqDM/go-ipfs-cmdkit"
	cmds "gx/ipfs/QmULd2tG5e3Hu6fdN1teSsXQFxzWmVWDmdMNMXutQnCbz9/go-ipfs-cmds"
)

const progressBarMinSize = 1024 * 1024 * 8 // show progress bar for outputs > 8MiB

var CatCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline:          "Show IPFS object data.",
		ShortDescription: "Displays the data contained by an IPFS or IPNS object(s) at the given path.",
	},

	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("ipfs-path", true, true, "The path to the IPFS object(s) to be outputted.").EnableStdin(),
	},
	Options: []cmdkit.Option{
		cmdkit.IntOption("offset", "o", "Byte offset to begin reading from."),
	},
	Run: func(req *cmds.Request, res cmds.ResponseEmitter, env interface{}) {
		node, err := GetNode(env)
		if err != nil {
			res.SetError(err, cmdkit.ErrNormal)
			return
		}

		if !node.OnlineMode() {
			if err := node.SetupOfflineRouting(); err != nil {
				res.SetError(err, cmdkit.ErrNormal)
				return
			}
		}

		offset, _ := req.Options["offset"].(int)
		if offset < 0 {
			res.SetError(fmt.Errorf("Cannot specify negative offset."), cmdkit.ErrNormal)
			return
		}

		err = req.ParseBodyArgs()
		if err != nil && err.Error() != "all arguments covered by positional arguments" {
			res.SetError(err, cmdkit.ErrNormal)
			return
		}

		readers, length, err := cat(req.Context, node, req.Arguments, int64(offset))
		if err != nil {
			res.SetError(err, cmdkit.ErrNormal)
			return
		}

		/*
			if err := corerepo.ConditionalGC(req.Context, node, length); err != nil {
				re.SetError(err, cmdkit.ErrNormal)
				return
			}
		*/

		res.SetLength(length)
		reader := io.MultiReader(readers...)

		// Since the reader returns the error that a block is missing, and that error is
		// returned from io.Copy inside Emit, we need to take Emit errors and send
		// them to the client. Usually we don't do that because it means the connection
		// is broken or we supplied an illegal argument etc.
		err = res.Emit(reader)
		if err != nil {
			res.SetError(err, cmdkit.ErrNormal)
		}
	},
	PostRun: map[cmds.EncodingType]func(*cmds.Request, cmds.ResponseEmitter) cmds.ResponseEmitter{
		cmds.CLI: func(req *cmds.Request, re cmds.ResponseEmitter) cmds.ResponseEmitter {
			reNext, res := cmds.NewChanResponsePair(req)

			go func() {
				if res.Length() > 0 && res.Length() < progressBarMinSize {
					if err := cmds.Copy(re, res); err != nil {
						re.SetError(err, cmdkit.ErrNormal)
					}

					return
				}

				// Copy closes by itself, so we must not do this before
				defer re.Close()
				for {
					v, err := res.Next()
					if !cmds.HandleError(err, res, re) {
						break
					}

					switch val := v.(type) {
					case io.Reader:
						bar, reader := progressBarForReader(os.Stderr, val, int64(res.Length()))
						bar.Start()

						err = re.Emit(reader)
						if err != nil {
							log.Error(err)
						}
					default:
						log.Warningf("cat postrun: received unexpected type %T", val)
					}
				}
			}()

			return reNext
		},
	},
}

func cat(ctx context.Context, node *core.IpfsNode, paths []string, offset int64) ([]io.Reader, uint64, error) {
	readers := make([]io.Reader, 0, len(paths))
	length := uint64(0)
	for _, fpath := range paths {
		read, err := coreunix.Cat(ctx, node, fpath)
		if err != nil {
			return nil, 0, err
		}
		if offset > int64(read.Size()) {
			offset = offset - int64(read.Size())
			continue
		}
		count, err := read.Seek(offset, io.SeekStart)
		if err != nil {
			return nil, 0, err
		}
		offset = 0

		readers = append(readers, read)
		length += uint64(read.Size() - uint64(count))
	}
	return readers, length, nil
}
