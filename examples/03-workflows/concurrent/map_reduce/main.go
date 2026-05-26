package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Map Reduce Workflow",
	"This sample uses fan-out and fan-in to count words with file-backed intermediate results.",
)

const (
	dataToProcessKey = "data_to_be_processed"
	mapReduceScope   = "MapReduceState"
)

var tempDir = filepath.Join(os.TempDir(), "workflow_viz_sample")

type chunkRange struct {
	Start int
	End   int
}

type (
	SplitComplete   struct{}
	MapComplete     struct{ FilePath string }
	ShuffleComplete struct {
		FilePath  string
		ReducerID string
	}
)
type ReduceComplete struct{ FilePath string }

func main() {
	if err := os.RemoveAll(tempDir); err != nil {
		demo.Panic(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	mapperIDs := []string{"map_executor_0", "map_executor_1", "map_executor_2"}
	reducerIDs := []string{"reduce_executor_0", "reduce_executor_1", "reduce_executor_2", "reduce_executor_3"}

	splitter := newSplitter("split_data_executor", mapperIDs)
	mappers := make([]workflow.ExecutorBinding, 0, len(mapperIDs))
	for _, id := range mapperIDs {
		mappers = append(mappers, newMapper(id))
	}
	shuffler := newShuffler("shuffle_executor", len(mapperIDs), reducerIDs)
	reducers := make([]workflow.ExecutorBinding, 0, len(reducerIDs))
	for _, id := range reducerIDs {
		reducers = append(reducers, newReducer(id))
	}
	completion := newCompletion("completion_executor", len(reducers))

	wf, err := workflow.NewBuilder(splitter).
		AddFanOutEdge(splitter, mappers).
		AddFanInBarrierEdge(mappers, shuffler).
		AddFanOutEdge(shuffler, reducers).
		AddFanInBarrierEdge(reducers, completion).
		WithOutputFrom(completion).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	input := inputText()
	run, err := inproc.Default.RunStreaming(context.Background(), wf, input)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(context.Background()) }()

	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			paths := e.Output.([]string)
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					demo.Panic(err)
				}
				demo.Assistantf("%s\n%s", filepath.Base(path), strings.TrimSpace(string(data)))
			}
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
}

func inputText() string {
	for _, path := range []string{
		filepath.Join("resources", "long_text.txt"),
		filepath.Join("examples", "03-workflows", "resources", "long_text.txt"),
		filepath.Join("..", "..", "..", "..", "resources", "long_text.txt"),
	} {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}
	demo.Assistant("Note: resources/long_text.txt not found, using sample text")
	return "The quick brown fox jumps over the lazy dog. The dog was very lazy. The fox was very quick."
}

func newSplitter(id string, mapperIDs []string) workflow.ExecutorBinding {
	return workflow.NewExecutor(id, func(ctx *workflow.Context, text string) error {
		if err := os.MkdirAll(tempDir, 0o755); err != nil {
			return err
		}
		words := preprocess(text)
		if err := ctx.QueueStateUpdate(dataToProcessKey, mapReduceScope, words); err != nil {
			return err
		}
		chunkSize := len(words) / len(mapperIDs)
		for index, mapperID := range mapperIDs {
			start := index * chunkSize
			end := start + chunkSize
			if index == len(mapperIDs)-1 {
				end = len(words)
			}
			if err := ctx.QueueStateUpdate(mapperID, mapReduceScope, chunkRange{Start: start, End: end}); err != nil {
				return err
			}
			if err := ctx.SendMessage(mapperID, SplitComplete{}); err != nil {
				return err
			}
		}
		return nil
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[SplitComplete]())
			return rb, nil
		},
	}).Bind()
}

func newMapper(id string) workflow.ExecutorBinding {
	return workflow.NewExecutor(id, func(ctx *workflow.Context, _ SplitComplete) error {
		wordsValue, err := ctx.ReadState(dataToProcessKey, mapReduceScope)
		if err != nil {
			return err
		}
		rangeValue, err := ctx.ReadState(id, mapReduceScope)
		if err != nil {
			return err
		}
		words := wordsValue.([]string)
		chunk := rangeValue.(chunkRange)
		path := filepath.Join(tempDir, "map_results_"+id+".txt")
		var lines []string
		for _, word := range words[chunk.Start:chunk.End] {
			lines = append(lines, word+": 1")
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return err
		}
		return ctx.SendMessage("", MapComplete{FilePath: path})
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[MapComplete]())
			return rb, nil
		},
	}).Bind()
}

func newShuffler(id string, expected int, reducerIDs []string) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		var mapResults []MapComplete
		return workflow.NewExecutor(executorID, func(ctx *workflow.Context, msg MapComplete) error {
			mapResults = append(mapResults, msg)
			if len(mapResults) < expected {
				return nil
			}
			groups, err := loadMapGroups(mapResults)
			if err != nil {
				return err
			}
			chunks := partitionGroups(groups, len(reducerIDs))
			for index, reducerID := range reducerIDs {
				path := filepath.Join(tempDir, fmt.Sprintf("shuffle_results_%d.txt", index))
				var lines []string
				for _, group := range chunks[index] {
					values, err := json.Marshal(group.Values)
					if err != nil {
						return err
					}
					lines = append(lines, fmt.Sprintf("%s: %s", group.Key, values))
				}
				if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
					return err
				}
				if err := ctx.SendMessage(reducerID, ShuffleComplete{FilePath: path, ReducerID: reducerID}); err != nil {
					return err
				}
			}
			mapResults = nil
			return nil
		}).Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[ShuffleComplete]())
				return rb, nil
			},
		}), nil
	})
}

func newReducer(id string) workflow.ExecutorBinding {
	return workflow.NewExecutor(id, func(ctx *workflow.Context, msg ShuffleComplete) error {
		if msg.ReducerID != id {
			return nil
		}
		data, err := os.ReadFile(msg.FilePath)
		if err != nil {
			return err
		}
		var lines []string
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			key, encoded, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			var values []int
			if err := json.Unmarshal([]byte(encoded), &values); err != nil {
				return err
			}
			total := 0
			for _, value := range values {
				total += value
			}
			lines = append(lines, fmt.Sprintf("%s: %d", key, total))
		}
		path := filepath.Join(tempDir, "reduced_results_"+id+".txt")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return err
		}
		return ctx.SendMessage("", ReduceComplete{FilePath: path})
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[ReduceComplete]())
			return rb, nil
		},
	}).Bind()
}

func newCompletion(id string, expected int) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		var paths []string
		return workflow.NewExecutor(executorID, func(ctx *workflow.Context, msg ReduceComplete) error {
			paths = append(paths, msg.FilePath)
			if len(paths) < expected {
				return nil
			}
			out := append([]string(nil), paths...)
			paths = nil
			return ctx.YieldOutput(out)
		}).Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[[]string]())
				return rb, nil
			},
		}), nil
	})
}

func preprocess(text string) []string {
	var words []string
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' })
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, word := range strings.Split(line, " ") {
			if strings.TrimSpace(word) != "" {
				words = append(words, word)
			}
		}
	}
	return words
}

type wordGroup struct {
	Key    string
	Values []int
}

func partitionGroups(groups map[string][]int, count int) [][]wordGroup {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	partitions := make([][]wordGroup, count)
	if count == 0 {
		return partitions
	}
	chunkSize := len(keys) / count
	remaining := len(keys) % count
	start := 0
	for index := range partitions {
		size := chunkSize
		if index == count-1 {
			size += remaining
		}
		end := min(start+size, len(keys))
		for _, key := range keys[start:end] {
			partitions[index] = append(partitions[index], wordGroup{Key: key, Values: groups[key]})
		}
		start = end
	}
	return partitions
}

func loadMapGroups(results []MapComplete) (map[string][]int, error) {
	groups := map[string][]int{}
	for _, result := range results {
		data, err := os.ReadFile(result.FilePath)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			key, value, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			count, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			groups[key] = append(groups[key], count)
		}
	}
	return groups, nil
}
