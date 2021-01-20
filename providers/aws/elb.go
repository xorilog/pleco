package aws

import (
	"errors"
	"fmt"
	"github.com/Qovery/pleco/utils"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elbv2"
	log "github.com/sirupsen/logrus"
	"strconv"
	"strings"
	"time"
)

type ElasticLoadBalancer struct {
	Arn string
	Name string
	CreatedTime time.Time
	Status string
	TTL int64
}

func TagLoadBalancersForDeletion(lbSession elbv2.ELBV2, tagKey string, loadBalancersList []ElasticLoadBalancer) error {
	var lbArns []*string

	if len(loadBalancersList) == 0 {
		return nil
	}

	for _, lb := range loadBalancersList {
		lbArns = append(lbArns, aws.String(lb.Arn))
	}

	_, err := lbSession.AddTags(
		&elbv2.AddTagsInput{
			ResourceArns: lbArns,
			Tags:         []*elbv2.Tag{
				{
					Key: aws.String(tagKey),
					Value: aws.String("1"),
				},
			},
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func ListTaggedLoadBalancersWithKeyContains(lbSession elbv2.ELBV2, tagContains string) ([]ElasticLoadBalancer, error) {
	var taggedLoadBalancers []ElasticLoadBalancer

	allLoadBalancers, err := ListLoadBalancers(lbSession)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Error while getting loadbalancer list on region %s\n", *lbSession.Config.Region))
	}

	// get lb tags and identify if one belongs to
	for _, currentLb := range allLoadBalancers {
		input := elbv2.DescribeTagsInput{ResourceArns: []*string{&currentLb.Arn}}

		result, err := lbSession.DescribeTags(&input)
		if err != nil {
			log.Errorf("Error while getting load balancer tags from %s", currentLb.Name)
			continue
		}

		for _, contentTag := range result.TagDescriptions[0].Tags {
			if strings.Contains(*contentTag.Key, tagContains) || strings.Contains(*contentTag.Value, tagContains) {
				taggedLoadBalancers = append(taggedLoadBalancers, currentLb)
			}
		}
	}

	return taggedLoadBalancers, nil
}

func listTaggedLoadBalancers(lbSession elbv2.ELBV2, tagName string) ([]ElasticLoadBalancer, error) {
	var taggedLoadBalancers []ElasticLoadBalancer

	allLoadBalancers, err := ListLoadBalancers(lbSession)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Error while getting loadbalancer list on region %s\n", *lbSession.Config.Region))
	}

	// get tag with ttl
	for _, currentLb := range allLoadBalancers {
		input := elbv2.DescribeTagsInput{ResourceArns: []*string{&currentLb.Arn}}

		result, err := lbSession.DescribeTags(&input)
		if err != nil {
			log.Errorf("Error while getting load balancer tags from %s", currentLb.Name)
			continue
		}

		for _, contentTag := range result.TagDescriptions[0].Tags {
			if *contentTag.Value == tagName {
				ttlInt, err := strconv.Atoi(*contentTag.Value)
				if err != nil {
					log.Errorf("Bad %s value on load balancer %s (%s), can't use it, it should be a number", tagName, currentLb.Name, *lbSession.Config.Region)
					continue
				}
				currentLb.TTL = int64(ttlInt)
				taggedLoadBalancers = append(taggedLoadBalancers, currentLb)
			}
		}
	}

	return taggedLoadBalancers, nil
}

func ListLoadBalancers(lbSession elbv2.ELBV2) ([]ElasticLoadBalancer, error) {
	var allLoadBalancers []ElasticLoadBalancer
	region := *lbSession.Config.Region

	log.Debugf("Listing all Loadbalancers in region %s", region)
	input := elbv2.DescribeLoadBalancersInput{}

	result, err := lbSession.DescribeLoadBalancers(&input)
	if err != nil {
		return nil, err
	}

	if len(result.LoadBalancers) == 0 {
		return nil, nil
	}

	for _, currentLb := range result.LoadBalancers {
		allLoadBalancers = append(allLoadBalancers, ElasticLoadBalancer{
			Arn:         *currentLb.LoadBalancerArn,
			Name:        *currentLb.LoadBalancerName,
			CreatedTime: *currentLb.CreatedTime,
			Status:      *currentLb.State.Code,
			TTL: 		 int64(-1),
		})
	}

	return allLoadBalancers, nil
}

func deleteLoadBalancers(lbSession elbv2.ELBV2, loadBalancersList []ElasticLoadBalancer, dryRun bool) error {
	if dryRun {
		return nil
	}

	if len(loadBalancersList) == 0 {
		return nil
	}

	for _, lb := range loadBalancersList {
		_, err := lbSession.DeleteLoadBalancer(
			&elbv2.DeleteLoadBalancerInput{LoadBalancerArn: &lb.Arn},
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func DeleteExpiredLoadBalancers(elbSession elbv2.ELBV2, tagName string, dryRun bool) error {
	lbs, err := listTaggedLoadBalancers(elbSession, tagName)
	if err != nil {
		return errors.New(fmt.Sprintf("can't list Load Balancers: %s\n", err))
	}

	for _, lb := range lbs {
		if utils.CheckIfExpired(lb.CreatedTime, lb.TTL) {
			err := deleteLoadBalancers(elbSession, lbs, dryRun)
			if err != nil {
				log.Errorf("Deletion ELB %s (%s) error: %s",
					lb.Name, *elbSession.Config.Region, err)
				continue
			}
		} else {
			log.Debugf("Load Balancer %s in %s, has not yet expired",
				lb.Name, *elbSession.Config.Region)
		}
	}

	return nil
}