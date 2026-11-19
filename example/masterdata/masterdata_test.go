package masterdata_test

import (
	"testing"
	"time"

	"github.com/moznion/go-datlas/example/masterdata"
	"github.com/moznion/go-datlas/example/model"
)

func TestMasterData(t *testing.T) {
	admin, ok := masterdata.RoleByCode("admin")
	if !ok || admin.Name != "Administrator" {
		t.Fatalf("RoleByCode(admin) = %+v, %v", admin, ok)
	}
	if _, ok := masterdata.RoleByCode("nobody"); ok {
		t.Fatal("RoleByCode(nobody) found a role")
	}
	pro, ok := masterdata.PlanByCode("pro")
	if !ok || pro.Trial != 30*24*time.Hour || pro.Limits.Seats != 50 || pro.Description == nil || pro.Tier != masterdata.TierBusiness {
		t.Fatalf("unexpected pro plan: %+v", pro)
	}
	if _, ok := masterdata.PermissionByRoleAndAction("member", "write"); ok {
		t.Fatal("member must not be able to write")
	}
	if _, ok := masterdata.PermissionByRoleAndAction("admin", "write"); !ok {
		t.Fatal("admin must be able to write")
	}
}

func TestMasterDataIsImmutable(t *testing.T) {
	plans := masterdata.Plans()
	plans[0].Name = "changed"
	plans[0].Features[0] = "changed"
	pro, _ := masterdata.PlanByCode("pro")
	*pro.Description = "changed"

	if again := masterdata.Plans(); again[0].Name == "changed" || again[0].Features[0] == "changed" {
		t.Fatal("modifying a returned value changed the master data")
	}
	if again, _ := masterdata.PlanByCode("pro"); *again.Description == "changed" {
		t.Fatal("modifying a looked up value changed the master data")
	}
}

func TestTextUnmarshalerKey(t *testing.T) {
	var tokyo model.UUID
	// Any case decodes to the same key.
	if err := tokyo.UnmarshalText([]byte("0191D3A0-7B1C-7C3E-9A4F-2B6D8E0F1A23")); err != nil {
		t.Fatal(err)
	}
	r, ok := masterdata.RegionByID(tokyo)
	if !ok || r.Code != "tokyo" {
		t.Fatalf("RegionByID(%s) = %+v, %v", tokyo, r, ok)
	}

	pro, _ := masterdata.PlanByCode("pro")
	if len(pro.Regions) != 2 || pro.Regions[0] != tokyo {
		t.Fatalf("unexpected regions of pro: %v", pro.Regions)
	}
	if virginia, ok := masterdata.RegionByID(pro.Regions[1]); !ok || virginia.Code != "virginia" {
		t.Fatalf("RegionByID(%s) = %+v, %v", pro.Regions[1], virginia, ok)
	}
}
