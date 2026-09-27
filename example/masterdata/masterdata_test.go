package masterdata_test

import (
	"testing"
	"time"

	"github.com/moznion/go-datlas/example/masterdata"
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
