package main

import (
	"testing"
)

func TestComputeResponsiveSidebarWidth(t *testing.T) {
	// Default params matching shell defaults: mobileMax=110, tabletMax=170, mobile=15, tablet=20, desktop=25, maxPercent=20, minContent=40
	tests := []struct {
		name         string
		windowWidth  int
		mobileMax    int
		tabletMax    int
		mobileWidth  int
		tabletWidth  int
		desktopWidth int
		maxPercent   int
		minContent   int
		want         int
	}{
		{
			name:         "desktop wide terminal",
			windowWidth:  200,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   40,
			want:         25,
		},
		{
			name:         "tablet terminal",
			windowWidth:  140,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   40,
			want:         20,
		},
		{
			name:         "mobile terminal",
			windowWidth:  100,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   40,
			want:         15,
		},
		{
			name:         "very narrow terminal",
			windowWidth:  50,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   40,
			want:         15,
		},
		{
			name:         "wide desktop custom width",
			windowWidth:  300,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 30,
			maxPercent:   20,
			minContent:   40,
			want:         30,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeResponsiveSidebarWidth(tc.windowWidth, tc.mobileMax, tc.tabletMax, tc.mobileWidth, tc.tabletWidth, tc.desktopWidth, tc.maxPercent, tc.minContent)
			if got != tc.want {
				t.Errorf("computeResponsiveSidebarWidth(%d,...) = %d, want %d", tc.windowWidth, got, tc.want)
			}
		})
	}
}

func TestComputeResponsiveSidebarWidthEdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		windowWidth  int
		mobileMax    int
		tabletMax    int
		mobileWidth  int
		tabletWidth  int
		desktopWidth int
		maxPercent   int
		minContent   int
		want         int
	}{
		{
			name:         "boundary: exactly at tablet threshold",
			windowWidth:  170,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   40,
			want:         20,
		},
		{
			name:         "boundary: just above tablet threshold",
			windowWidth:  171,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   40,
			want:         25,
		},
		{
			name:         "boundary: exactly at mobile threshold",
			windowWidth:  110,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   40,
			want:         15,
		},
		{
			name:         "boundary: just above mobile threshold",
			windowWidth:  111,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   40,
			want:         20,
		},
		{
			name:         "custom maxPercent: 30%",
			windowWidth:  100,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   30,
			minContent:   40,
			want:         15,
		},
		{
			name:         "custom minContent: 50",
			windowWidth:  100,
			mobileMax:    110,
			tabletMax:    170,
			mobileWidth:  15,
			tabletWidth:  20,
			desktopWidth: 25,
			maxPercent:   20,
			minContent:   50,
			want:         15,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeResponsiveSidebarWidth(tc.windowWidth, tc.mobileMax, tc.tabletMax, tc.mobileWidth, tc.tabletWidth, tc.desktopWidth, tc.maxPercent, tc.minContent)
			if got != tc.want {
				t.Errorf("computeResponsiveSidebarWidth(%d,...) = %d, want %d", tc.windowWidth, got, tc.want)
			}
		})
	}
}
