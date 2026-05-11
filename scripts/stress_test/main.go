// Stress test: simula N operadores enviando msgs simultaneamente via webhook
// ONIMCONNECTORMESSAGEADD do Bitrix24, igual ao formato real que chega em
// /bitrix/connector/event.
//
// Uso:
//   go run ./scripts/stress_test \
//     -url https://uctalk.uctechnology.com.br/bitrix/connector/event \
//     -connector wa_cloud_1160607470462388 \
//     -line 220 \
//     -concurrent 50 \
//     -msgs-per-conv 1
//
// Reporta latencia p50/p95/p99, taxa de sucesso e total no fim.
package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	latency time.Duration
	status  int
	err     error
}

func main() {
	endpoint := flag.String("url", "http://localhost:3000/bitrix/connector/event", "Endpoint do connector event")
	connector := flag.String("connector", "wa_cloud_1160607470462388", "Connector ID")
	line := flag.Int("line", 220, "Open Line ID")
	imChatBase := flag.Int("im-chat-base", 9000, "Base do im.chat_id (cada conversa adiciona +offset)")
	concurrent := flag.Int("concurrent", 50, "Quantas conversas simultaneas")
	msgsPerConv := flag.Int("msgs-per-conv", 1, "Msgs por conversa (sequencial dentro de cada goroutine)")
	timeoutSec := flag.Int("timeout", 30, "Timeout HTTP por request em segundos")
	flag.Parse()

	fmt.Printf("=== Stress Test ===\n")
	fmt.Printf("URL:          %s\n", *endpoint)
	fmt.Printf("Connector:    %s (line %d)\n", *connector, *line)
	fmt.Printf("Concurrent:   %d conversas\n", *concurrent)
	fmt.Printf("Msgs/conv:    %d\n", *msgsPerConv)
	fmt.Printf("Total reqs:   %d\n\n", *concurrent*(*msgsPerConv))

	client := &http.Client{Timeout: time.Duration(*timeoutSec) * time.Second}
	resultsCh := make(chan result, *concurrent*(*msgsPerConv))

	var sent atomic.Int64
	startAll := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < *concurrent; i++ {
		wg.Add(1)
		go func(convIdx int) {
			defer wg.Done()
			// Cada conversa fictícia tem um cliente único: numero base + offset
			clientPhone := fmt.Sprintf("5519%07d", 9000000+convIdx)
			chatID := clientPhone + "@s.whatsapp.net"
			imChatID := *imChatBase + convIdx

			for m := 0; m < *msgsPerConv; m++ {
				imMsgID := 800000 + convIdx*1000 + m
				body := buildFormBody(*connector, *line, chatID, imChatID, imMsgID,
					fmt.Sprintf("Stress test msg #%d conv %d %d", m, convIdx, rand.Intn(99999)))
				start := time.Now()
				status, err := postForm(client, *endpoint, body)
				latency := time.Since(start)
				resultsCh <- result{latency: latency, status: status, err: err}
				sent.Add(1)
			}
		}(i)
	}

	// Goroutine de progresso simples
	doneProgress := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				fmt.Printf("  ... %d/%d enviadas (%.1fs)\n",
					sent.Load(), int64(*concurrent*(*msgsPerConv)),
					time.Since(startAll).Seconds())
			case <-doneProgress:
				return
			}
		}
	}()

	wg.Wait()
	close(doneProgress)
	close(resultsCh)
	elapsed := time.Since(startAll)

	// Coleta resultados
	var latencies []time.Duration
	successCount, failCount, errCount := 0, 0, 0
	statusCounts := map[int]int{}
	for r := range resultsCh {
		if r.err != nil {
			errCount++
			continue
		}
		latencies = append(latencies, r.latency)
		statusCounts[r.status]++
		if r.status >= 200 && r.status < 300 {
			successCount++
		} else {
			failCount++
		}
	}

	if len(latencies) == 0 {
		fmt.Println("Nenhuma requisição completou com sucesso.")
		os.Exit(1)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)*50/100]
	p95 := latencies[len(latencies)*95/100]
	p99 := latencies[(len(latencies)*99)/100]
	maxL := latencies[len(latencies)-1]
	minL := latencies[0]
	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	avg := sum / time.Duration(len(latencies))

	total := *concurrent * (*msgsPerConv)
	throughput := float64(total) / elapsed.Seconds()

	fmt.Printf("\n=== Resultado ===\n")
	fmt.Printf("Tempo total:  %.2fs\n", elapsed.Seconds())
	fmt.Printf("Throughput:   %.1f req/s\n", throughput)
	fmt.Printf("Sucesso:      %d/%d (%.1f%%)\n", successCount, total, 100.0*float64(successCount)/float64(total))
	fmt.Printf("Falhas HTTP:  %d\n", failCount)
	fmt.Printf("Erros rede:   %d\n", errCount)
	fmt.Printf("Status codes: %v\n\n", statusCounts)

	fmt.Printf("Latencia:\n")
	fmt.Printf("  min:  %v\n", minL)
	fmt.Printf("  avg:  %v\n", avg)
	fmt.Printf("  p50:  %v\n", p50)
	fmt.Printf("  p95:  %v\n", p95)
	fmt.Printf("  p99:  %v\n", p99)
	fmt.Printf("  max:  %v\n", maxL)
}

// buildFormBody monta o body form-urlencoded EXATAMENTE como o Bitrix envia
// no ONIMCONNECTORMESSAGEADD (form-encoded com chaves entre colchetes).
func buildFormBody(connector string, line int, chatID string, imChatID, imMsgID int, text string) string {
	v := url.Values{}
	v.Set("event", "ONIMCONNECTORMESSAGEADD")
	v.Set("event_handler_id", "stress")
	v.Set("data[CONNECTOR]", connector)
	v.Set("data[LINE]", strconv.Itoa(line))
	v.Set("data[MESSAGES][0][im][chat_id]", strconv.Itoa(imChatID))
	v.Set("data[MESSAGES][0][im][message_id]", strconv.Itoa(imMsgID))
	v.Set("data[MESSAGES][0][message][user_id]", "80")
	v.Set("data[MESSAGES][0][message][text]", text)
	v.Set("data[MESSAGES][0][chat][id]", chatID)
	v.Set("ts", strconv.FormatInt(time.Now().Unix(), 10))
	// auth fields — o handler não valida no caminho normal, mas envia
	// estes 5 que sempre vêm para deixar bem fiel ao real.
	v.Set("auth[access_token]", "stress_test_token")
	v.Set("auth[domain]", "uctdemo.bitrix24.com")
	v.Set("auth[member_id]", "stress")
	v.Set("auth[application_token]", "stress")
	v.Set("auth[user_id]", "80")
	return v.Encode()
}

func postForm(client *http.Client, endpoint, body string) (int, error) {
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
