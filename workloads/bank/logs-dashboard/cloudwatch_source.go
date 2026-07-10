package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// discoverAgentLogGroup finds the CloudWatch log group for an AgentCore Runtime
// by name prefix. The Runtime's actual log group name embeds an AWS-assigned
// ID with a random suffix (e.g. "cofide_bank_demo_agent-j6I3ly7uEj") that isn't
// knowable ahead of time from Terraform config alone, so this discovers it via
// the API rather than guessing/hardcoding a naming convention.
func discoverAgentLogGroup(ctx context.Context, client *cloudwatchlogs.Client, agentRuntimeName string) (string, error) {
	prefix := "/aws/bedrock-agentcore/runtimes/" + agentRuntimeName
	out, err := client.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: &prefix,
	})
	if err != nil {
		return "", fmt.Errorf("describe log groups: %w", err)
	}
	if len(out.LogGroups) == 0 {
		return "", fmt.Errorf("no CloudWatch log group found with prefix %q — pass --agent-log-group explicitly if the agent has never been invoked yet, or if it uses a different naming convention", prefix)
	}
	// If more than one exists (e.g. multiple runtime versions), the most
	// recently created is almost always the one currently in use.
	newest := out.LogGroups[0]
	for _, lg := range out.LogGroups[1:] {
		if lg.CreationTime != nil && (newest.CreationTime == nil || *lg.CreationTime > *newest.CreationTime) {
			newest = lg
		}
	}
	return *newest.LogGroupName, nil
}

// runCloudWatchTail polls a CloudWatch log group for new events and publishes
// them to h, forever. Polling (not the newer StartLiveTail streaming API) is
// used deliberately: it's a handful of lines of well-understood request/
// response code rather than a bidirectional event-stream client, which is
// more than this tool needs for demo-quality "a few seconds of latency is
// fine" log tailing.
func runCloudWatchTail(ctx context.Context, h *hub, client *cloudwatchlogs.Client, logGroupName string, pollInterval time.Duration) {
	h.publish(fmt.Sprintf("--- tailing CloudWatch log group %s ---", logGroupName))

	startTime := time.Now().Add(-10 * time.Minute).UnixMilli()
	seenAtStartTime := map[string]bool{}
	missingGroupWarned := false

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		events, err := fetchNewEvents(ctx, client, logGroupName, startTime)
		if err != nil {
			var rnf *types.ResourceNotFoundException
			if errors.As(err, &rnf) {
				if !missingGroupWarned {
					h.publish("--- log group not found yet — will keep checking (this is normal before the first invocation) ---")
					missingGroupWarned = true
				}
				continue
			}
			h.publish(fmt.Sprintf("--- error fetching CloudWatch logs: %s ---", err))
			continue
		}
		missingGroupWarned = false

		for _, e := range events {
			ts := *e.Timestamp
			id := *e.EventId
			if ts == startTime && seenAtStartTime[id] {
				continue
			}
			h.publish(formatCloudWatchEvent(e))
			if ts > startTime {
				startTime = ts
				seenAtStartTime = map[string]bool{}
			}
			seenAtStartTime[id] = true
		}
	}
}

func fetchNewEvents(ctx context.Context, client *cloudwatchlogs.Client, logGroupName string, startTime int64) ([]types.FilteredLogEvent, error) {
	var events []types.FilteredLogEvent
	var nextToken *string
	for {
		out, err := client.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName: &logGroupName,
			StartTime:    &startTime,
			NextToken:    nextToken,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, out.Events...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return events, nil
}

func formatCloudWatchEvent(e types.FilteredLogEvent) string {
	ts := time.UnixMilli(*e.Timestamp).Format("15:04:05.000")
	return fmt.Sprintf("%s  %s", ts, *e.Message)
}
