Feature: Working out each plugin setting from the environment and its default
  A downstream application lists the settings each plugin declares in its
  plugin.json. After the check, it looks up the environment variable of
  each setting and works out each value from the values it found, reading
  no environment itself. A value the environment holds wins over the
  default, an empty value counts as unset, and a value its type rejects is
  named by its variable. A secret comes from the environment only and
  prints as set or not set, never as itself.

  Background:
    Given the application names plugin variables under "APP_PLUGIN_"
    And the plugin "notes" declares these settings:
      | key      | type     | default | min | max | options        |
      | title    | text     | Notes   |     |     |                |
      | per_page | count    | 10      | 1   | 50  |                |
      | open     | flag     | false   |     |     |                |
      | keep_for | duration | 720h    |     |     |                |
      | order    | choice   | newest  |     |     | newest, oldest |
      | tags     | lines    | news    |     |     |                |
      | api_key  | secret   |         |     |     |                |

  Scenario: Each setting is read from a variable named after its plugin and its key
    Then the settings of "notes" are read from these variables:
      | key      | variable                   |
      | title    | APP_PLUGIN_NOTES__TITLE    |
      | per_page | APP_PLUGIN_NOTES__PER_PAGE |
      | open     | APP_PLUGIN_NOTES__OPEN     |
      | keep_for | APP_PLUGIN_NOTES__KEEP_FOR |
      | order    | APP_PLUGIN_NOTES__ORDER    |
      | tags     | APP_PLUGIN_NOTES__TAGS     |
      | api_key  | APP_PLUGIN_NOTES__API_KEY  |

  Scenario Outline: Each hyphen of a plugin id becomes an underscore in the variable
    Given the plugin "<id>" declares the text setting "max_seats"
    Then the setting "max_seats" of "<id>" is read from "<variable>"

    Examples:
      | id             | variable                             |
      | events         | APP_PLUGIN_EVENTS__MAX_SEATS         |
      | events-pro     | APP_PLUGIN_EVENTS_PRO__MAX_SEATS     |
      | events-pro-max | APP_PLUGIN_EVENTS_PRO_MAX__MAX_SEATS |

  Scenario: Each setting takes its default when the environment holds none
    When the settings of "notes" are worked out
    Then the settings read:
      | key      | value   |
      | title    | Notes   |
      | per_page | 10      |
      | open     | false   |
      | keep_for | 720h    |
      | order    | newest  |
      | tags     | news    |
      | api_key  | not set |

  Scenario Outline: A value in the environment wins over the default
    Given the environment holds "<value>" in "<variable>"
    When the settings of "notes" are worked out
    Then "<key>" reads "<read>"

    Examples:
      | key      | variable                   | value           | read         |
      | title    | APP_PLUGIN_NOTES__TITLE    | Field Notes     | Field Notes  |
      | per_page | APP_PLUGIN_NOTES__PER_PAGE | 25              | 25           |
      | open     | APP_PLUGIN_NOTES__OPEN     | true            | true         |
      | keep_for | APP_PLUGIN_NOTES__KEEP_FOR | 48h             | 48h          |
      | order    | APP_PLUGIN_NOTES__ORDER    | oldest          | oldest       |
      | tags     | APP_PLUGIN_NOTES__TAGS     | news,, events   | news, events |
      | api_key  | APP_PLUGIN_NOTES__API_KEY  | key-for-example | set          |

  Scenario: An empty value counts as unset
    Given the environment holds "" in "APP_PLUGIN_NOTES__PER_PAGE"
    And the environment holds "" in "APP_PLUGIN_NOTES__API_KEY"
    When the settings of "notes" are worked out
    Then "per_page" reads "10"
    And "api_key" reads "not set"

  Scenario Outline: A value its type rejects is named by its variable
    Given the environment holds "<value>" in "<variable>"
    When the settings of "notes" are worked out
    Then the settings are rejected with "<reason>"

    Examples:
      | variable                   | value  | reason                                                               |
      | APP_PLUGIN_NOTES__PER_PAGE | banana | APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "banana"     |
      | APP_PLUGIN_NOTES__PER_PAGE | 2.5    | APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "2.5"        |
      | APP_PLUGIN_NOTES__PER_PAGE | 60     | APP_PLUGIN_NOTES__PER_PAGE: must stand at or below 50, got "60"      |
      | APP_PLUGIN_NOTES__PER_PAGE | 0      | APP_PLUGIN_NOTES__PER_PAGE: must stand at or above 1, got "0"        |
      | APP_PLUGIN_NOTES__KEEP_FOR | soon   | APP_PLUGIN_NOTES__KEEP_FOR: must be a duration like 30s, got "soon"  |
      | APP_PLUGIN_NOTES__KEEP_FOR | -1h    | APP_PLUGIN_NOTES__KEEP_FOR: must stand above zero, got "-1h"         |
      | APP_PLUGIN_NOTES__OPEN     | maybe  | APP_PLUGIN_NOTES__OPEN: must be true or false, got "maybe"           |
      | APP_PLUGIN_NOTES__ORDER    | random | APP_PLUGIN_NOTES__ORDER: must be one of newest, oldest, got "random" |

  Scenario: The first rejected value in the manifest's order is the one named
    Given the environment holds "banana" in "APP_PLUGIN_NOTES__PER_PAGE"
    And the environment holds "random" in "APP_PLUGIN_NOTES__ORDER"
    When the settings of "notes" are worked out
    Then the settings are rejected with "APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "banana""

  Scenario: A secret reads its value only when asked for and prints as set
    Given the environment holds "key-for-example" in "APP_PLUGIN_NOTES__API_KEY"
    When the settings of "notes" are worked out
    Then the secret "api_key" reads "key-for-example" when asked for
    And every print or log of the settings shows "set" for "api_key"
    And no print, log or encoding of the settings holds "key-for-example"

  Scenario: A value rejected beside a secret never shows the secret
    Given the environment holds "key-for-example" in "APP_PLUGIN_NOTES__API_KEY"
    And the environment holds "banana" in "APP_PLUGIN_NOTES__PER_PAGE"
    When the settings of "notes" are worked out
    Then the settings are rejected with "APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "banana""
    And the reason does not hold "key-for-example"
