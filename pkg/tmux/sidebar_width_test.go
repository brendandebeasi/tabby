package tmux

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeResponsiveSidebarWidth_Desktop(t *testing.T) {
	// windowWidth > tabletMax → return desktopWidth
	result := ComputeResponsiveSidebarWidth(200, 110, 170, 15, 20, 25, 20, 40)
	assert.Equal(t, 25, result)
}

func TestComputeResponsiveSidebarWidth_Tablet(t *testing.T) {
	// windowWidth > mobileMax but <= tabletMax → return tabletWidth
	result := ComputeResponsiveSidebarWidth(140, 110, 170, 15, 20, 25, 20, 40)
	assert.Equal(t, 20, result)
}

func TestComputeResponsiveSidebarWidth_TabletClampsTo15(t *testing.T) {
	// tabletWidth < 15 → clamp to 15
	result := ComputeResponsiveSidebarWidth(140, 110, 170, 15, 10, 25, 20, 40)
	assert.Equal(t, 15, result)
}

func TestComputeResponsiveSidebarWidth_Mobile_FractionWins(t *testing.T) {
	// Mobile path: maxByFraction < mobileWidth and < maxByContent
	// windowWidth=80, maxPercent=20 → maxByFraction = 80*20/100 = 16
	// maxByContent = 80-40 = 40
	// mobileWidth = 20
	// min(16, 40, 20) = 16
	result := ComputeResponsiveSidebarWidth(80, 110, 170, 20, 20, 25, 20, 40)
	assert.Equal(t, 16, result)
}

func TestComputeResponsiveSidebarWidth_Mobile_ContentWins(t *testing.T) {
	// Mobile path: maxByContent < maxByFraction and < mobileWidth
	// windowWidth=60, maxPercent=20 → maxByFraction = 60*20/100 = 12 → clamped to 15
	// maxByContent = 60-40 = 20
	// mobileWidth = 25
	// min(15, 20, 25) = 15
	result := ComputeResponsiveSidebarWidth(60, 110, 170, 25, 20, 25, 20, 40)
	assert.Equal(t, 15, result)
}

func TestComputeResponsiveSidebarWidth_Mobile_MobileWidthWins(t *testing.T) {
	// Mobile path: mobileWidth is smallest
	// windowWidth=100, maxPercent=20 → maxByFraction = 100*20/100 = 20
	// maxByContent = 100-40 = 60
	// mobileWidth = 15
	// min(20, 60, 15) = 15
	result := ComputeResponsiveSidebarWidth(100, 110, 170, 15, 20, 25, 20, 40)
	assert.Equal(t, 15, result)
}

func TestComputeResponsiveSidebarWidth_Mobile_ClampTo10(t *testing.T) {
	// Very narrow window, result clamped to 10
	// windowWidth=50, maxPercent=10 → maxByFraction = 50*10/100 = 5 → clamped to 15
	// maxByContent = 50-40 = 10
	// mobileWidth = 8
	// min(15, 10, 8) = 8 → clamped to 10
	result := ComputeResponsiveSidebarWidth(50, 110, 170, 8, 20, 25, 10, 40)
	assert.Equal(t, 10, result)
}

func TestComputeResponsiveSidebarWidth_Mobile_FractionClampTo15(t *testing.T) {
	// Mobile path: maxByFraction < 15 → clamped to 15
	// windowWidth=70, maxPercent=10 → maxByFraction = 70*10/100 = 7 → clamped to 15
	// maxByContent = 70-40 = 30
	// mobileWidth = 25
	// min(15, 30, 25) = 15
	result := ComputeResponsiveSidebarWidth(70, 110, 170, 25, 20, 25, 10, 40)
	assert.Equal(t, 15, result)
}

func TestComputeResponsiveSidebarWidth_Mobile_ContentClampTo15(t *testing.T) {
	// Mobile path: maxByContent < 15 → clamped to 15
	// windowWidth=50, maxPercent=20 → maxByFraction = 50*20/100 = 10 → clamped to 15
	// maxByContent = 50-40 = 10 → clamped to 15
	// mobileWidth = 20
	// min(15, 15, 20) = 15
	result := ComputeResponsiveSidebarWidth(50, 110, 170, 20, 20, 25, 20, 40)
	assert.Equal(t, 15, result)
}
