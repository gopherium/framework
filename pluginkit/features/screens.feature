Feature: Admin menus and lazy screens from the manifest
  A plugin's manifest can list its admin menu entries, name the module
  that holds its admin screens, and name the capability each screen needs
  when no menu entry links to it. The wiring generator checks the shape of
  those keys. When the application asks for it, the generator also writes
  a file that imports a plugin's screens module only when one of its
  screens opens, so an admin with many plugins never downloads every
  plugin's screens at once. A screens module does not replace the
  frontend, so a plugin can carry both while it moves.

  Scenario: The menu, the screens module and the screen capabilities are read as data
    Given the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    And "events" has the menu entry {"label": "Reservations", "screen": "reservations", "capability": "events.manage"}
    And "events" names "events.manage" for the screen "seats"
    When the manifests are read
    Then the manifest of "events" has the menu entry {"label": "Reservations", "screen": "reservations", "capability": "events.manage"}
    And the manifest of "events" names the screens module "@example/plugin-events/screens"
    And the manifest of "events" names "events.manage" for the screen "seats"
    And the manifest of "events" keeps no extra key

  Scenario: The lazy screens file imports each plugin's screens module in wiring order
    Given the plugin "billing" has a backend
    And the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    And the plugin "gallery" has a backend and the screens module "@example/plugin-gallery/screens"
    And the application asks for a lazy screens file
    When the plugins are wired
    Then the lazy screens file maps these plugins, in order:
      | plugin  | module                          |
      | events  | @example/plugin-events/screens  |
      | gallery | @example/plugin-gallery/screens |

  Scenario: A plugin that keeps its frontend loads as before beside one that moved
    Given the plugin "events" has the frontend "@example/plugin-events" and the screens module "@example/plugin-events/screens"
    And the plugin "gallery" has the frontend "@example/plugin-gallery"
    And the application asks for a lazy screens file
    When the plugins are wired
    Then the frontend wiring imports "@example/plugin-events" and "@example/plugin-gallery"
    And the lazy screens file maps these plugins, in order:
      | plugin | module                         |
      | events | @example/plugin-events/screens |

  Scenario: The lazy screens file is an empty map when no plugin names a screens module
    Given the plugin "gallery" has the frontend "@example/plugin-gallery"
    And the application asks for a lazy screens file
    When the plugins are wired
    Then the lazy screens file maps no plugin

  Scenario: Without a lazy screens file the generator writes only today's files
    Given the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    When the plugins are wired
    Then the generator writes only the Go and the TypeScript wiring

  Scenario: A screens module does not count as a frontend
    Given the plugin "events" has only the screens module "@example/plugin-events/screens"
    When the plugins are wired
    Then the wiring is refused with: pluginwire: plugins/events/plugin.json: at least one of backend or frontend is required

  Scenario Outline: A plugin with a screens module keeps its package in the package.json
    Given the application keeps its plugin packages in a package.json
    And the plugin "events" has a backend and the screens module "<module>"
    When the plugins are wired
    Then the package.json depends only on "<package>" as a workspace package

    Examples:
      | module                         | package                |
      | @example/plugin-events/screens | @example/plugin-events |
      | @example/plugin-events         | @example/plugin-events |
      | plugin-events/screens          | plugin-events          |
      | plugin-events                  | plugin-events          |

  Scenario: A frontend and a screens module from one package are one dependency
    Given the application keeps its plugin packages in a package.json
    And the plugin "events" has the frontend "@example/plugin-events" and the screens module "@example/plugin-events/screens"
    When the plugins are wired
    Then the package.json depends only on "@example/plugin-events" as a workspace package

  Scenario Outline: A screens module that names no package is refused while a package.json is kept
    Given the application keeps its plugin packages in a package.json
    And the plugin "events" has a backend and the screens module "<module>"
    When the plugins are wired
    Then the wiring is refused with: pluginwire: package.json: events: the screens module "<module>" names no package
    And the files of the earlier wiring are left as they were

    Examples:
      | module                        |
      | ./screens                     |
      | /admin/screens/events.js      |
      | @example                      |
      | https://example.com/events.js |

  Scenario: A screens module with no package is kept when the application keeps no package.json
    Given the plugin "events" has a backend and the screens module "./screens"
    And the application asks for a lazy screens file
    When the plugins are wired
    Then the lazy screens file maps these plugins, in order:
      | plugin | module    |
      | events | ./screens |

  Scenario Outline: A menu entry with a wrong shape is refused
    Given the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    And "events" has the menu entry <entry>
    When the plugins are wired
    Then the wiring is refused with: pluginwire: plugins/events/plugin.json: <error>
    And the files of the earlier wiring are left as they were

    Examples:
      | entry                                                                                                   | error                                                                                   |
      | {"screen": "reservations", "capability": "events.manage"}                                               | menu[0]: label is required                                                              |
      | {"label": "Reservations", "capability": "events.manage"}                                                | menu[0]: screen is required                                                             |
      | {"label": "Reservations", "screen": "reservations"}                                                     | menu[0]: capability is required                                                         |
      | {"label": "Reservations", "screen": "reservations", "capability": ""}                                   | menu[0]: capability is required                                                         |
      | {"label": "Reservations", "screen": "list/all", "capability": "events.manage"}                          | menu[0]: the screen "list/all" must be one path segment                                 |
      | {"label": "Reservations", "screen": "reservations", "capability": "events.manage", "position": "first"} | json: cannot unmarshal string into Go struct field manifest.menu.0.position of type int |

  Scenario Outline: A screen capability with a wrong shape is refused
    Given the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    And "events" has the screen capabilities <capabilities>
    When the plugins are wired
    Then the wiring is refused with: pluginwire: plugins/events/plugin.json: <error>

    Examples:
      | capabilities                   | error                                                                                               |
      | {"seats": ""}                  | screen_capabilities: the screen "seats" needs a capability                                          |
      | {"": "events.manage"}          | screen_capabilities: the screen "" must be one path segment                                         |
      | {"seats/all": "events.manage"} | screen_capabilities: the screen "seats/all" must be one path segment                                |
      | {"seats": 3}                   | json: cannot unmarshal number into Go struct field manifest.screen_capabilities.seats of type string |

  Scenario: A screen capability for a screen a menu entry names is refused
    Given the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    And "events" has the menu entry {"label": "Reservations", "screen": "reservations", "capability": "events.manage"}
    And "events" names "events.view" for the screen "reservations"
    When the plugins are wired
    Then the wiring is refused with: pluginwire: plugins/events/plugin.json: screen_capabilities: the screen "reservations" takes the capability of menu[0]

  Scenario: Two menu entries that ask different capabilities for one screen are refused
    Given the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    And "events" has the menu entry {"label": "Reservations", "screen": "reservations", "capability": "events.manage"}
    And "events" has the menu entry {"label": "Bookings", "screen": "reservations", "parent": "content", "capability": "events.view"}
    When the plugins are wired
    Then the wiring is refused with: pluginwire: plugins/events/plugin.json: menu[1]: the screen "reservations" asks for "events.view" but menu[0] asks for "events.manage"

  Scenario: Two menu entries can open one screen that asks for one capability
    Given the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    And "events" has the menu entry {"label": "Reservations", "screen": "reservations", "capability": "events.manage"}
    And "events" has the menu entry {"label": "Bookings", "screen": "reservations", "parent": "content", "capability": "events.manage"}
    When the manifests are read
    Then the manifest of "events" has the menu entry {"label": "Bookings", "screen": "reservations", "parent": "content", "capability": "events.manage"}

  Scenario: The manifest index writes the menu, the screens module and the screen capabilities
    Given the plugin "events" has a backend and the screens module "@example/plugin-events/screens"
    And "events" has the menu entry {"label": "Reservations", "screen": "reservations", "parent": "content", "capability": "events.manage"}
    And "events" names "events.manage" for the screen "seats"
    And the application asks for a manifest index
    When the plugins are wired
    Then the manifest index holds this entry for "events":
      """
      {
        "id": "events",
        "name": "events",
        "backend": "example.com/plugins/events",
        "menu": [{"label": "Reservations", "screen": "reservations", "parent": "content", "capability": "events.manage"}],
        "screens": "@example/plugin-events/screens",
        "screen_capabilities": {"seats": "events.manage"}
      }
      """
