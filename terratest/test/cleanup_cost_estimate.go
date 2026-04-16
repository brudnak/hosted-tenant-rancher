package test

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/pricing"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/spf13/viper"
)

type cleanupCostEstimate struct {
	Region                string
	EstimatedEC2CostUSD   float64
	EstimatedEBSCostUSD   float64
	EstimatedRDSCostUSD   float64
	EC2Lines              []cleanupEC2Line
	EBSLines              []cleanupEBSLine
	RDSLines              []cleanupRDSLine
	RDSStorageNotIncluded bool
}

type cleanupEC2Line struct {
	InstanceType      string
	Count             int
	TotalRuntimeHours float64
	HourlyRateUSD     float64
	EstimatedCostUSD  float64
}

type cleanupEBSLine struct {
	VolumeType        string
	VolumeCount       int
	VolumeSizeGiB     int64
	TotalRuntimeHours float64
	MonthlyRateUSD    float64
	EstimatedCostUSD  float64
}

type cleanupRDSLine struct {
	DBClass           string
	Engine            string
	Count             int
	TotalRuntimeHours float64
	HourlyRateUSD     float64
	EstimatedCostUSD  float64
}

func estimateCurrentRunCost(totalInstances int, outputs map[string]string) (*cleanupCostEstimate, error) {
	sess, region, err := newCleanupCostSession()
	if err != nil {
		return nil, err
	}

	instances, err := resolveCleanupEC2Instances(sess, totalInstances, outputs)
	if err != nil {
		return nil, err
	}

	rdsInstances, err := resolveCleanupRDSInstances(sess, totalInstances, outputs)
	if err != nil {
		return nil, err
	}

	return buildCleanupCostEstimate(sess, region, instances, rdsInstances)
}

func newCleanupCostSession() (*session.Session, string, error) {
	region := strings.TrimSpace(viper.GetString("tf_vars.aws_region"))
	if region == "" {
		region = strings.TrimSpace(viper.GetString("s3.region"))
	}
	if region == "" {
		region = "us-east-2"
	}

	cfg := aws.NewConfig().WithRegion(region)
	accessKey := strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY"))
	if accessKey != "" && secretKey != "" {
		cfg = cfg.WithCredentials(credentials.NewStaticCredentials(accessKey, secretKey, ""))
	}

	sess, err := session.NewSession(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create AWS session for cleanup estimate: %w", err)
	}

	return sess, region, nil
}

func resolveCleanupEC2Instances(sess *session.Session, totalInstances int, outputs map[string]string) ([]*ec2.Instance, error) {
	ec2Client := ec2.New(sess)
	seenIPs := map[string]bool{}
	instances := make([]*ec2.Instance, 0, totalInstances*2)

	for i := 0; i < totalInstances; i++ {
		for _, ip := range []string{
			outputs[fmt.Sprintf("infra%d_server1_ip", i+1)],
			outputs[fmt.Sprintf("infra%d_server2_ip", i+1)],
		} {
			ip = strings.TrimSpace(ip)
			if ip == "" || seenIPs[ip] {
				continue
			}
			seenIPs[ip] = true

			output, err := ec2Client.DescribeInstances(&ec2.DescribeInstancesInput{
				Filters: []*ec2.Filter{
					{
						Name:   aws.String("ip-address"),
						Values: []*string{aws.String(ip)},
					},
					{
						Name: aws.String("instance-state-name"),
						Values: []*string{
							aws.String(ec2.InstanceStateNamePending),
							aws.String(ec2.InstanceStateNameRunning),
							aws.String(ec2.InstanceStateNameStopping),
							aws.String(ec2.InstanceStateNameStopped),
						},
					},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to describe EC2 instance for IP %s: %w", ip, err)
			}

			found := false
			for _, reservation := range output.Reservations {
				for _, instance := range reservation.Instances {
					instances = append(instances, instance)
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("no EC2 instance found for public IP %s", ip)
			}
		}
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("no EC2 instances found for cleanup estimate")
	}

	return instances, nil
}

func resolveCleanupRDSInstances(sess *session.Session, totalInstances int, outputs map[string]string) ([]*rds.DBInstance, error) {
	rdsClient := rds.New(sess)
	expectedHosts := map[string]bool{}
	for i := 0; i < totalInstances; i++ {
		endpoint := strings.TrimSpace(outputs[fmt.Sprintf("infra%d_mysql_endpoint", i+1)])
		if endpoint == "" {
			continue
		}
		host := strings.Split(endpoint, ":")[0]
		expectedHosts[host] = true
		expectedHosts[endpoint] = true
	}

	if len(expectedHosts) == 0 {
		return nil, nil
	}

	found := []*rds.DBInstance{}
	err := rdsClient.DescribeDBInstancesPages(&rds.DescribeDBInstancesInput{}, func(page *rds.DescribeDBInstancesOutput, lastPage bool) bool {
		for _, dbInstance := range page.DBInstances {
			if dbInstance == nil || dbInstance.Endpoint == nil {
				continue
			}
			address := strings.TrimSpace(aws.StringValue(dbInstance.Endpoint.Address))
			hostPort := address
			if address != "" && dbInstance.Endpoint.Port != nil {
				hostPort = fmt.Sprintf("%s:%d", address, aws.Int64Value(dbInstance.Endpoint.Port))
			}
			if expectedHosts[address] || expectedHosts[hostPort] {
				found = append(found, dbInstance)
			}
		}
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe RDS instances for cleanup estimate: %w", err)
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("no RDS instances found for cleanup estimate")
	}

	return found, nil
}

func buildCleanupCostEstimate(sess *session.Session, region string, instances []*ec2.Instance, rdsInstances []*rds.DBInstance) (*cleanupCostEstimate, error) {
	now := time.Now()
	estimate := &cleanupCostEstimate{Region: region}

	type ec2Agg struct {
		count        int
		runtimeHours float64
	}
	ec2Aggs := map[string]*ec2Agg{}

	volumeRuntimeHours := map[string]float64{}
	ec2VolumeIDs := make([]*string, 0)
	seenVolumeIDs := map[string]bool{}

	for _, instance := range instances {
		instanceType := aws.StringValue(instance.InstanceType)
		if instanceType == "" {
			continue
		}

		runtimeHours := 0.0
		if instance.LaunchTime != nil {
			runtimeHours = now.Sub(aws.TimeValue(instance.LaunchTime)).Hours()
		}

		agg := ec2Aggs[instanceType]
		if agg == nil {
			agg = &ec2Agg{}
			ec2Aggs[instanceType] = agg
		}
		agg.count++
		agg.runtimeHours += runtimeHours

		for _, mapping := range instance.BlockDeviceMappings {
			if mapping.Ebs == nil || mapping.Ebs.VolumeId == nil {
				continue
			}
			volumeID := aws.StringValue(mapping.Ebs.VolumeId)
			if volumeID == "" {
				continue
			}
			volumeRuntimeHours[volumeID] += runtimeHours
			if !seenVolumeIDs[volumeID] {
				seenVolumeIDs[volumeID] = true
				ec2VolumeIDs = append(ec2VolumeIDs, aws.String(volumeID))
			}
		}
	}

	for _, instanceType := range sortedKeys(ec2Aggs) {
		agg := ec2Aggs[instanceType]
		hourlyRateUSD, err := lookupEC2OnDemandHourlyPriceUSD(sess, region, instanceType)
		if err != nil {
			return nil, err
		}
		cost := hourlyRateUSD * agg.runtimeHours
		estimate.EstimatedEC2CostUSD += cost
		estimate.EC2Lines = append(estimate.EC2Lines, cleanupEC2Line{
			InstanceType:      instanceType,
			Count:             agg.count,
			TotalRuntimeHours: agg.runtimeHours,
			HourlyRateUSD:     hourlyRateUSD,
			EstimatedCostUSD:  cost,
		})
	}

	if len(ec2VolumeIDs) > 0 {
		ec2Client := ec2.New(sess)
		volumesOutput, err := ec2Client.DescribeVolumes(&ec2.DescribeVolumesInput{VolumeIds: ec2VolumeIDs})
		if err != nil {
			return nil, fmt.Errorf("failed to describe EBS volumes for cleanup estimate: %w", err)
		}

		type ebsAgg struct {
			count        int
			sizeGiB      int64
			runtimeHours float64
		}
		ebsAggs := map[string]*ebsAgg{}

		for _, volume := range volumesOutput.Volumes {
			volumeID := aws.StringValue(volume.VolumeId)
			volumeType := aws.StringValue(volume.VolumeType)
			sizeGiB := aws.Int64Value(volume.Size)
			key := fmt.Sprintf("%s|%d", volumeType, sizeGiB)

			agg := ebsAggs[key]
			if agg == nil {
				agg = &ebsAgg{sizeGiB: sizeGiB}
				ebsAggs[key] = agg
			}
			agg.count++
			agg.runtimeHours += volumeRuntimeHours[volumeID]
		}

		for _, key := range sortedKeys(ebsAggs) {
			agg := ebsAggs[key]
			parts := strings.SplitN(key, "|", 2)
			volumeType := parts[0]
			monthlyRateUSD, err := lookupEBSMonthlyPricePerGiBUSD(sess, region, volumeType)
			if err != nil {
				return nil, err
			}
			cost := monthlyRateUSD * float64(agg.sizeGiB*int64(agg.count)) * (agg.runtimeHours / 730.0)
			estimate.EstimatedEBSCostUSD += cost
			estimate.EBSLines = append(estimate.EBSLines, cleanupEBSLine{
				VolumeType:        volumeType,
				VolumeCount:       agg.count,
				VolumeSizeGiB:     agg.sizeGiB,
				TotalRuntimeHours: agg.runtimeHours,
				MonthlyRateUSD:    monthlyRateUSD,
				EstimatedCostUSD:  cost,
			})
		}
	}

	type rdsAgg struct {
		engine       string
		count        int
		runtimeHours float64
	}
	rdsAggs := map[string]*rdsAgg{}
	for _, dbInstance := range rdsInstances {
		if dbInstance == nil {
			continue
		}
		dbClass := aws.StringValue(dbInstance.DBInstanceClass)
		if dbClass == "" {
			continue
		}
		engine := normalizeRDSEngine(aws.StringValue(dbInstance.Engine))
		key := dbClass + "|" + engine

		agg := rdsAggs[key]
		if agg == nil {
			agg = &rdsAgg{engine: engine}
			rdsAggs[key] = agg
		}
		agg.count++
		if dbInstance.InstanceCreateTime != nil {
			agg.runtimeHours += now.Sub(aws.TimeValue(dbInstance.InstanceCreateTime)).Hours()
		}
	}

	for _, key := range sortedKeys(rdsAggs) {
		agg := rdsAggs[key]
		parts := strings.SplitN(key, "|", 2)
		dbClass := parts[0]
		hourlyRateUSD, err := lookupRDSOnDemandHourlyPriceUSD(sess, region, dbClass, agg.engine)
		if err != nil {
			return nil, err
		}
		cost := hourlyRateUSD * agg.runtimeHours
		estimate.EstimatedRDSCostUSD += cost
		estimate.RDSLines = append(estimate.RDSLines, cleanupRDSLine{
			DBClass:           dbClass,
			Engine:            agg.engine,
			Count:             agg.count,
			TotalRuntimeHours: agg.runtimeHours,
			HourlyRateUSD:     hourlyRateUSD,
			EstimatedCostUSD:  cost,
		})
	}

	estimate.RDSStorageNotIncluded = len(rdsInstances) > 0
	return estimate, nil
}

func lookupEC2OnDemandHourlyPriceUSD(sess *session.Session, region, instanceType string) (float64, error) {
	pricingClient := pricing.New(sess, aws.NewConfig().WithRegion("us-east-1"))
	location, err := awsPricingLocation(region)
	if err != nil {
		return 0, err
	}

	output, err := pricingClient.GetProducts(&pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		MaxResults:  aws.Int64(100),
		Filters: []*pricing.Filter{
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("location"), Value: aws.String(location)},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("instanceType"), Value: aws.String(instanceType)},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("operatingSystem"), Value: aws.String("Linux")},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("tenancy"), Value: aws.String("Shared")},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("preInstalledSw"), Value: aws.String("NA")},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("capacitystatus"), Value: aws.String("Used")},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query EC2 pricing: %w", err)
	}

	return extractUSDPriceFromPricingResult(output.PriceList)
}

func lookupEBSMonthlyPricePerGiBUSD(sess *session.Session, region, volumeType string) (float64, error) {
	pricingClient := pricing.New(sess, aws.NewConfig().WithRegion("us-east-1"))
	location, err := awsPricingLocation(region)
	if err != nil {
		return 0, err
	}

	output, err := pricingClient.GetProducts(&pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		MaxResults:  aws.Int64(100),
		Filters: []*pricing.Filter{
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("location"), Value: aws.String(location)},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("productFamily"), Value: aws.String("Storage")},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("volumeApiName"), Value: aws.String(volumeType)},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query EBS pricing: %w", err)
	}

	return extractUSDPriceFromPricingResult(output.PriceList)
}

func lookupRDSOnDemandHourlyPriceUSD(sess *session.Session, region, dbClass, engine string) (float64, error) {
	pricingClient := pricing.New(sess, aws.NewConfig().WithRegion("us-east-1"))
	location, err := awsPricingLocation(region)
	if err != nil {
		return 0, err
	}

	output, err := pricingClient.GetProducts(&pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonRDS"),
		MaxResults:  aws.Int64(100),
		Filters: []*pricing.Filter{
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("location"), Value: aws.String(location)},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("productFamily"), Value: aws.String("Database Instance")},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("instanceType"), Value: aws.String(dbClass)},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("databaseEngine"), Value: aws.String(engine)},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("deploymentOption"), Value: aws.String("Single-AZ")},
			{Type: aws.String(pricing.FilterTypeTermMatch), Field: aws.String("licenseModel"), Value: aws.String("No license required")},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query RDS pricing: %w", err)
	}

	return extractUSDPriceFromPricingResult(output.PriceList)
}

func extractUSDPriceFromPricingResult(priceList []aws.JSONValue) (float64, error) {
	type pricingDocument struct {
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}

	bestPrice := math.MaxFloat64
	for _, item := range priceList {
		raw, err := json.Marshal(item)
		if err != nil {
			continue
		}

		var doc pricingDocument
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}

		for _, offer := range doc.Terms.OnDemand {
			for _, dimension := range offer.PriceDimensions {
				usdValue := strings.TrimSpace(dimension.PricePerUnit["USD"])
				if usdValue == "" {
					continue
				}
				price, err := strconv.ParseFloat(usdValue, 64)
				if err != nil {
					continue
				}
				if price > 0 && price < bestPrice {
					bestPrice = price
				}
			}
		}
	}

	if bestPrice == math.MaxFloat64 {
		return 0, fmt.Errorf("no USD price found in AWS pricing response")
	}

	return bestPrice, nil
}

func awsPricingLocation(region string) (string, error) {
	locations := map[string]string{
		"us-east-1": "US East (N. Virginia)",
		"us-east-2": "US East (Ohio)",
		"us-west-1": "US West (N. California)",
		"us-west-2": "US West (Oregon)",
	}
	location := locations[region]
	if location == "" {
		return "", fmt.Errorf("no AWS pricing location mapping configured for region %s", region)
	}
	return location, nil
}

func logCleanupCostEstimate(estimate *cleanupCostEstimate) {
	totalEstimatedUSD := estimate.EstimatedEC2CostUSD + estimate.EstimatedEBSCostUSD + estimate.EstimatedRDSCostUSD
	log.Printf("[cleanup] Estimated AWS cost for this run (live pricing):")
	log.Printf("[cleanup] Region: %s", estimate.Region)

	for _, line := range estimate.EC2Lines {
		log.Printf("[cleanup] EC2: %d x %s over %.2f total hours at $%.4f/hour -> $%.2f estimated",
			line.Count, line.InstanceType, line.TotalRuntimeHours, line.HourlyRateUSD, line.EstimatedCostUSD)
	}

	for _, line := range estimate.EBSLines {
		log.Printf("[cleanup] EBS: %d x %d GiB %s over %.2f total hours at $%.4f/GiB-month -> $%.2f estimated",
			line.VolumeCount, line.VolumeSizeGiB, line.VolumeType, line.TotalRuntimeHours, line.MonthlyRateUSD, line.EstimatedCostUSD)
	}

	for _, line := range estimate.RDSLines {
		log.Printf("[cleanup] RDS: %d x %s (%s) over %.2f total hours at $%.4f/hour -> $%.2f estimated",
			line.Count, line.DBClass, line.Engine, line.TotalRuntimeHours, line.HourlyRateUSD, line.EstimatedCostUSD)
	}

	if estimate.RDSStorageNotIncluded {
		log.Printf("[cleanup] Note: Aurora storage is usage-based and is not included in this estimate.")
	}

	log.Printf("[cleanup] Estimated total (EC2 + EBS + RDS instance-hours): $%.2f", totalEstimatedUSD)
}

func normalizeRDSEngine(engine string) string {
	switch strings.TrimSpace(strings.ToLower(engine)) {
	case "aurora-mysql":
		return "Aurora MySQL"
	default:
		return engine
	}
}

func sortedKeys[T any](input map[string]T) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
