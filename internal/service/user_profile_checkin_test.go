package service

import (
	"reflect"
	"testing"
)

func TestNormalizeCheckinRewardsFallsBackWhenAllRewardsAreZero(t *testing.T) {
	got := normalizeCheckinRewards([]int{0, 0, 0, 0, 0, 0, 0})
	want := []int{1, 1, 2, 2, 3, 3, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeCheckinRewards() = %#v, want %#v", got, want)
	}
}

func TestNormalizeCheckinRewardsKeepsPartiallyConfiguredRewards(t *testing.T) {
	got := normalizeCheckinRewards([]int{1, 0, 2})
	want := []int{1, 0, 2, 2, 3, 3, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeCheckinRewards() = %#v, want %#v", got, want)
	}
}
