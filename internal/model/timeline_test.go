package model

import "testing"

func TestNewTimelinePagePlan(t *testing.T) {
	plan := NewTimelinePagePlan("My Notes", 2, 5)
	if plan.BaseRoute != "/My%20Notes/" {
		t.Fatalf("BaseRoute = %q, want %q", plan.BaseRoute, "/My%20Notes/")
	}
	if plan.PageSize != 2 {
		t.Fatalf("PageSize = %d, want 2", plan.PageSize)
	}
	want := []TimelinePage{
		{Number: 1, Route: "/My%20Notes/", Start: 0, End: 2},
		{Number: 2, Route: "/My%20Notes/page/2/", Start: 2, End: 4},
		{Number: 3, Route: "/My%20Notes/page/3/", Start: 4, End: 5},
	}
	if len(plan.Pages) != len(want) {
		t.Fatalf("Pages length = %d, want %d", len(plan.Pages), len(want))
	}
	for i := range want {
		if plan.Pages[i] != want[i] {
			t.Errorf("Pages[%d] = %#v, want %#v", i, plan.Pages[i], want[i])
		}
	}
	page, ok := plan.PageForRoute("/My%20Notes/page/2/")
	if !ok || page.Number != 2 {
		t.Fatalf("PageForRoute() = %#v, %t; want page 2", page, ok)
	}
}

func TestNewTimelinePagePlanAlwaysHasAnEmptyPage(t *testing.T) {
	plan := NewTimelinePagePlan("notes", 20, 0)
	if plan.PageSize != 1 || len(plan.Pages) != 1 {
		t.Fatalf("empty plan = %#v, want page size 1 and one page", plan)
	}
	if plan.Pages[0].Start != 0 || plan.Pages[0].End != 0 || plan.Pages[0].Route != "/notes/" {
		t.Fatalf("empty page = %#v", plan.Pages[0])
	}
}
