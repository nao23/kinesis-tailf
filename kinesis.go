package ktail

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/nao23/kinesis-tailf/kpl"
	"github.com/vmihailenco/msgpack/v5"
	"log"
	"math/big"
	"os"
	"sync"
	"time"
)

var (
	flushInterval    = 100 * time.Millisecond
	iterateInterval  = time.Second
	LF               = []byte{'\n'}
	maxEmptyIterates = 100
)

type IterateParams struct {
	StreamName     string
	ShardID        string
	StartTimestamp time.Time
	EndTimestamp   time.Time
}

type isOverFunc func(time.Time, ...bool) bool

//go:generate protoc --go_out=kpl --go_opt=paths=source_relative ./kpl.proto

type App struct {
	kinesis         *kinesis.Client
	cfg             aws.Config
	StreamName      string
	AppendLF        bool
	DecodeAsMsgPack bool
}

func New(cfg aws.Config, name string) *App {
	return &App{
		kinesis:    kinesis.NewFromConfig(cfg),
		cfg:        cfg,
		StreamName: name,
	}
}

func (app *App) Run(ctx context.Context, shardKey string, startTs, endTs time.Time) error {
	shardIds, err := app.determinShardIds(ctx, shardKey)
	if err != nil {
		return err
	}

	var wg, wgW sync.WaitGroup
	ch := make(chan []byte, 1000)
	ctxC, cancel := context.WithCancel(ctx)
	wgW.Add(1)
	go app.writer(ctxC, ch, &wgW)

	for _, id := range shardIds {
		wg.Add(1)
		go func(id string) {
			param := IterateParams{
				ShardID:        id,
				StartTimestamp: startTs,
				EndTimestamp:   endTs,
			}
			err := app.iterate(ctx, param, ch)
			if err != nil {
				log.Println(err)
			}
			wg.Done()
		}(id)
	}
	wg.Wait()
	cancel()
	close(ch)
	wgW.Wait()
	return nil
}

func (app *App) iterate(ctx context.Context, p IterateParams, ch chan []byte) error {
	in := &kinesis.GetShardIteratorInput{
		ShardId:    aws.String(p.ShardID),
		StreamName: aws.String(app.StreamName),
	}
	if p.StartTimestamp.IsZero() {
		in.ShardIteratorType = types.ShardIteratorTypeLatest
	} else {
		in.ShardIteratorType = types.ShardIteratorTypeAtTimestamp
		in.Timestamp = &(p.StartTimestamp)
	}

	var isOver isOverFunc
	var emptyHits int
	if p.EndTimestamp.IsZero() {
		isOver = func(_ time.Time, _ ...bool) bool {
			return false
		}
	} else {
		isOver = func(t time.Time, empty ...bool) bool {
			if len(empty) > 0 && empty[0] {
				emptyHits++
				return maxEmptyIterates <= emptyHits
			}
			return p.EndTimestamp.Before(t)
		}
	}

	r, err := app.kinesis.GetShardIterator(ctx, in)
	if err != nil {
		return err
	}
	itr := r.ShardIterator
	for {
		rr, err := app.kinesis.GetRecords(ctx, &kinesis.GetRecordsInput{
			Limit:         aws.Int32(1000),
			ShardIterator: itr,
		})
		if err != nil {
			return err
		}
		itr = rr.NextShardIterator
		for _, record := range rr.Records {
			if isOver(*record.ApproximateArrivalTimestamp) {
				return nil
			}
			ar, err := kpl.Unmarshal(record.Data)
			if err == nil {
				for _, r := range ar.Records {
					ch <- r.Data
				}
			} else {
				ch <- record.Data
			}
		}
		if len(rr.Records) == 0 {
			if isOver(time.Now(), true) {
				return nil
			}
			time.Sleep(iterateInterval)
		}
	}
}

func (app *App) writer(ctx context.Context, ch chan []byte, wg *sync.WaitGroup) {
	defer wg.Done()
	var mu sync.Mutex

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	// run periodical flusher
	ticker := time.NewTicker(flushInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				w.Flush()
				mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		b, ok := <-ch
		if !ok {
			// channel closed
			return
		}
		mu.Lock()

		if app.DecodeAsMsgPack {
			var decoded interface{}
			err := msgpack.Unmarshal(b, &decoded)
			if err != nil {
				return
			}
			jsonBytes, err := json.Marshal(decoded)
			if err != nil {
				return
			}

			w.Write(jsonBytes)
		} else {
			w.Write(b)
		}

		if app.AppendLF {
			w.Write(LF)
		}
		mu.Unlock()
	}
}

func toHashKey(s string) *big.Int {
	b := md5.Sum([]byte(s))
	return big.NewInt(0).SetBytes(b[:])
}

func (app *App) determinShardIds(ctx context.Context, shardKey string) ([]string, error) {
	var shardIds []string

	sd, err := app.kinesis.DescribeStream(ctx, &kinesis.DescribeStreamInput{
		StreamName: aws.String(app.StreamName),
	})
	if err != nil {
		return shardIds, err
	}

	if shardKey == "" {
		// all shards
		for _, s := range sd.StreamDescription.Shards {
			shardIds = append(shardIds, *s.ShardId)
		}
		return shardIds, nil
	}

	hashKey := toHashKey(shardKey)

	for _, s := range sd.StreamDescription.Shards {
		start, end := big.NewInt(0), big.NewInt(0)
		start.SetString(*s.HashKeyRange.StartingHashKey, 10)
		end.SetString(*s.HashKeyRange.EndingHashKey, 10)

		if start.Cmp(hashKey) <= 0 && hashKey.Cmp(end) <= 0 {
			shardIds = append(shardIds, *s.ShardId)
			break
		}
	}
	return shardIds, nil
}
