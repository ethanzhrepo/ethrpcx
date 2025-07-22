package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"time"
)

// aggregateCall makes parallel calls to multiple endpoints and aggregates results
func (c *Client) aggregateCall(ctx context.Context, result interface{}, method string, args ...interface{}) error {
	// Get all non-closed endpoints
	c.mu.RLock()
	activeEndpoints := make([]*Endpoint, 0, len(c.endpoints))

	for _, endpoint := range c.endpoints {
		endpoint.mu.RLock()
		if !endpoint.IsClosed {
			activeEndpoints = append(activeEndpoints, endpoint)
		}
		endpoint.mu.RUnlock()
	}
	c.mu.RUnlock()

	if len(activeEndpoints) == 0 {
		// Try to connect to any endpoint
		if err := c.connectToAnyEndpoint(); err != nil {
			return err
		}

		// Get the newly connected endpoint
		endpoint, err := c.GetActiveEndpoint()
		if err != nil {
			return err
		}

		// Just do a simple call since we only have one endpoint
		return endpoint.RpcClient.CallContext(ctx, result, method, args...)
	}

	// If we only have one active endpoint, just use that
	if len(activeEndpoints) == 1 {
		return activeEndpoints[0].RpcClient.CallContext(ctx, result, method, args...)
	}

	// Create a child context with the same deadline
	var childCtx context.Context
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		// Use the parent's deadline
		remaining := time.Until(deadline)
		childCtx, cancel = context.WithTimeout(context.Background(), remaining)
	} else {
		// Use our default timeout
		childCtx, cancel = context.WithTimeout(context.Background(), c.config.Timeout)
	}
	defer cancel()

	// Define a result type to collect both successes and errors
	type endpointResult struct {
		endpoint *Endpoint
		value    interface{}
		jsonData []byte
		err      error
	}

	// Channel to collect results from all endpoints
	resultChan := make(chan endpointResult, len(activeEndpoints))

	// Number of endpoints
	endpointCount := len(activeEndpoints)
	majorityNeeded := (endpointCount / 2) + 1

	// Launch goroutines to call each endpoint
	for _, endpoint := range activeEndpoints {
		go func(ep *Endpoint) {
			// Create a new result value of the same type with optimized reflection
			resultVal := c.createResultValue(result)

			// Make the RPC call
			err := ep.RpcClient.CallContext(childCtx, resultVal, method, args...)
			ep.mu.Lock()
			ep.LastUsed = time.Now()
			ep.mu.Unlock()

			// Handle error case
			if err != nil {
				resultChan <- endpointResult{
					endpoint: ep,
					err:      err,
				}
				return
			}

			// Successful call, marshal for comparison
			jsonData, mErr := json.Marshal(resultVal)
			if mErr != nil {
				resultChan <- endpointResult{
					endpoint: ep,
					err:      mErr,
				}
				return
			}

			// Send successful result
			resultChan <- endpointResult{
				endpoint: ep,
				value:    resultVal,
				jsonData: jsonData,
			}
		}(endpoint)
	}

	// Process results
	responses := make(map[string][]interface{})
	errorsMap := make(map[string][]error)
	receivedCount := 0
	var firstResult interface{}
	var consensusResult interface{}
	var consensusReached bool

	// Wait for all results or timeout
	for {
		select {
		case res := <-resultChan:
			receivedCount++

			if res.err != nil {
				// Handle error
				errStr := res.err.Error()
				errorsMap[errStr] = append(errorsMap[errStr], res.err)
			} else {
				// Handle success
				jsonStr := string(res.jsonData)
				responses[jsonStr] = append(responses[jsonStr], res.value)

				// Store first result for faster response if needed
				if firstResult == nil {
					firstResult = res.value
				}

				// Check for consensus
				if len(responses[jsonStr]) >= majorityNeeded && !consensusReached {
					consensusReached = true
					consensusResult = res.value
				}
			}

			// If we have consensus already, or this is the first result and we prefer first results
			if consensusReached || (c.config.PreferFirstResult && firstResult != nil) {
				var selectedResult interface{}
				if consensusReached {
					selectedResult = consensusResult
				} else {
					selectedResult = firstResult
				}
				
				// Use optimized copy method
				if err := c.copyResult(result, selectedResult); err != nil {
					log.Printf("Failed to copy result: %v", err)
					// Fallback to reflection if copy fails
					reflect.ValueOf(result).Elem().Set(reflect.ValueOf(selectedResult).Elem())
				}

				// Start a goroutine to collect and log remaining results
				go func() {
					remaining := endpointCount - receivedCount
					if remaining > 0 {
						for i := 0; i < remaining; i++ {
							r := <-resultChan
							if r.err != nil {
								errStr := r.err.Error()
								errorsMap[errStr] = append(errorsMap[errStr], r.err)
							} else {
								jsonStr := string(r.jsonData)
								responses[jsonStr] = append(responses[jsonStr], r.value)
							}
						}
					}
					logAggregateResultStats(method, responsesToCountMap(responses), errorsMap)
				}()

				return nil
			}

			// If all endpoints have reported but no consensus
			if receivedCount == endpointCount {
				// Find the most common result
				var maxValues []interface{}
				for _, values := range responses {
					if len(values) > len(maxValues) {
						maxValues = values
					}
				}

				if len(maxValues) > 0 {
					// Use the most common result
					if err := c.copyResult(result, maxValues[0]); err != nil {
						log.Printf("Failed to copy result: %v", err)
						// Fallback to reflection if copy fails
						reflect.ValueOf(result).Elem().Set(reflect.ValueOf(maxValues[0]).Elem())
					}
					logAggregateResultStats(method, responsesToCountMap(responses), errorsMap)
					return nil
				}

				// No successful results, return most common error
				return getMostCommonError(errorsMap, method)
			}

		case <-childCtx.Done():
			// Timeout or cancellation
			// Check if we have any results despite the timeout
			if firstResult != nil {
				// Use any result we have
				if err := c.copyResult(result, firstResult); err != nil {
					log.Printf("Failed to copy result: %v", err)
					// Fallback to reflection if copy fails
					reflect.ValueOf(result).Elem().Set(reflect.ValueOf(firstResult).Elem())
				}
				go func() {
					// Collect any remaining results
					remaining := endpointCount - receivedCount
					for i := 0; i < remaining; i++ {
						select {
						case r := <-resultChan:
							if r.err != nil {
								errStr := r.err.Error()
								errorsMap[errStr] = append(errorsMap[errStr], r.err)
							} else {
								jsonStr := string(r.jsonData)
								responses[jsonStr] = append(responses[jsonStr], r.value)
							}
						default:
							// No more results available
							break
						}
					}
					logAggregateResultStats(method, responsesToCountMap(responses), errorsMap)
				}()
				return nil
			}

			// No results, return timeout error
			if len(errorsMap) > 0 {
				return getMostCommonError(errorsMap, method)
			}

			return ClassifyError(errors.New("aggregated call failed: timeout"), "multiple endpoints")
		}
	}
}

// Helper to convert from responses map to count map
func responsesToCountMap(responses map[string][]interface{}) map[string]int {
	countMap := make(map[string]int)
	for key, values := range responses {
		countMap[key] = len(values)
	}
	return countMap
}

// Helper to get the most common error
func getMostCommonError(errorsMap map[string][]error, method string) error {
	var maxKey string
	var maxCount int
	for key, errs := range errorsMap {
		if len(errs) > maxCount {
			maxKey = key
			maxCount = len(errs)
		}
	}
	return ClassifyError(fmt.Errorf("aggregated call to %s failed: %s", method, maxKey), "multiple endpoints")
}

// logAggregateResultStats logs statistics about the aggregated results
func logAggregateResultStats(method string, responses map[string]int, errors map[string][]error) {
	if len(responses) == 1 {
		return // All results were identical, nothing to log
	}

	// Count total responses and errors
	totalResponses := 0
	for _, count := range responses {
		totalResponses += count
	}

	totalErrors := 0
	for _, errs := range errors {
		totalErrors += len(errs)
	}

	// Create a sorted list of response counts for logging
	type resultCount struct {
		result string
		count  int
	}
	resultCounts := make([]resultCount, 0, len(responses))
	for result, count := range responses {
		resultCounts = append(resultCounts, resultCount{result, count})
	}
	sort.Slice(resultCounts, func(i, j int) bool {
		return resultCounts[i].count > resultCounts[j].count
	})

	// Log the statistics
	if len(resultCounts) > 1 {
		log.Printf("Aggregated call to %s received %d different responses (%d total responses, %d errors)",
			method, len(resultCounts), totalResponses, totalErrors)

		for i, rc := range resultCounts {
			if i < 2 { // Just log the top two different results
				log.Printf("  Response #%d: (count: %d) %s", i+1, rc.count, rc.result)
			}
		}
	}
}

// createResultValue creates a new result value with optimized reflection
func (c *Client) createResultValue(result interface{}) interface{} {
	resultType := reflect.TypeOf(result)
	if resultType.Kind() != reflect.Ptr {
		// This shouldn't happen with proper usage, but handle it gracefully
		return reflect.New(resultType).Interface()
	}
	
	elemType := resultType.Elem()
	return reflect.New(elemType).Interface()
}

// copyResult copies the result value with optimized reflection
func (c *Client) copyResult(dst, src interface{}) error {
	dstVal := reflect.ValueOf(dst)
	srcVal := reflect.ValueOf(src)
	
	if dstVal.Kind() != reflect.Ptr || srcVal.Kind() != reflect.Ptr {
		return errors.New("both dst and src must be pointers")
	}
	
	dstElem := dstVal.Elem()
	srcElem := srcVal.Elem()
	
	if !srcElem.Type().AssignableTo(dstElem.Type()) {
		return fmt.Errorf("source type %v is not assignable to destination type %v", 
			srcElem.Type(), dstElem.Type())
	}
	
	dstElem.Set(srcElem)
	return nil
}

// GetMajorityResponse returns the response that was returned by the majority of endpoints
func getMajorityResponse(responses map[string]int) (string, int) {
	var maxKey string
	var maxCount int
	for key, count := range responses {
		if count > maxCount {
			maxKey = key
			maxCount = count
		}
	}
	return maxKey, maxCount
}
