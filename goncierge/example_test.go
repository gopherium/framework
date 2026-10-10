// SPDX-License-Identifier: Apache-2.0

package goncierge_test

import (
	"fmt"

	"github.com/gopherium/framework/goncierge"
)

func ExampleRegistry() {
	registry := goncierge.New()
	if err := registry.Grant("core", "admin", "users.manage", "events.manage"); err != nil {
		return
	}
	if err := registry.Grant("core", "member"); err != nil {
		return
	}
	if err := registry.Grant("events", "organizer", "events.manage"); err != nil {
		return
	}

	fmt.Println(registry.Can("organizer", "events.manage"))
	fmt.Println(registry.Grantable("organizer"))
	fmt.Println(registry.HoldersOf("users.manage"))

	registry.Withdraw("events")
	fmt.Println(registry.Roles())
	// Output:
	// true
	// [organizer member]
	// [admin]
	// [admin member]
}

func ExampleRegistry_Grant() {
	err := goncierge.New().Grant("core", "Shop manager", "orders.manage")

	fmt.Println(err)
	// Output: goncierge: invalid name for a role: "Shop manager"
}
