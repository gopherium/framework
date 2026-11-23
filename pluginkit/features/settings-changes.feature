Feature: Checking a change to a plugin's settings before it is saved
  A downstream application lets an administrator change a plugin's
  settings, and checks each change against the plugin's settings and the
  environment before anything is saved. A change is accepted whole, or
  rejected at its first problem with its kind, its key and why: a key the
  plugin does not declare, a key the environment sets, a secret, which
  comes from the environment only, or a value its type rejects. Keys the
  plugin does not declare come first, sorted, then the declared fields in
  the manifest's order. The application turns the kind and the reason into
  its own answer.

  Background:
    Given the application names plugin variables under "APP_PLUGIN_"
    And the plugin "notes" declares these settings:
      | key      | type     | default | min | max | options        |
      | title    | text     | Notes   |     |     |                |
      | per_page | count    | 10      | 1   | 50  |                |
      | total    | count    | 0       |     |     |                |
      | open     | flag     | false   |     |     |                |
      | keep_for | duration | 720h    |     |     |                |
      | order    | choice   | newest  |     |     | newest, oldest |
      | tags     | lines    | news    |     |     |                |
      | api_key  | secret   |         |     |     |                |

  Scenario: A change to declared settings is accepted
    When a change to "notes" sets these settings:
      | key      | value        |
      | title    | Field Notes  |
      | per_page | 25           |
      | open     | true         |
      | keep_for | 48h          |
      | order    | oldest       |
      | tags     | news, events |
    Then the change is accepted

  Scenario: A change to a key the plugin does not declare is rejected as unknown
    When a change to "notes" sets "colour" to "green"
    Then the change is rejected as "unknown" for "colour"

  Scenario: A change to a field the environment sets is rejected as locked, naming its variable
    Given the environment holds "Garden Notes" in "APP_PLUGIN_NOTES__TITLE"
    When a change to "notes" sets "title" to "Field Notes"
    Then the change is rejected as "locked" for "title"
    And the rejection names the variable "APP_PLUGIN_NOTES__TITLE"

  Scenario: A blank value in the environment locks nothing
    Given the environment holds "  " in "APP_PLUGIN_NOTES__TITLE"
    When a change to "notes" sets "title" to "Field Notes"
    Then the change is accepted

  Scenario Outline: A value its type rejects is rejected as invalid with its reason
    When a change to "notes" sets "<key>" to "<value>"
    Then the change is rejected as "invalid" for "<key>" with the reason "<reason>"

    Examples:
      | key      | value  | reason       |
      | per_page | banana | not_whole    |
      | per_page | 2.5    | not_whole    |
      | per_page | 0      | below_min    |
      | per_page | 60     | above_max    |
      | open     | maybe  | not_flag     |
      | keep_for | soon   | not_duration |
      | keep_for | -1h    | not_positive |
      | order    | random | not_option   |

  Scenario Outline: A count out of its bounds gives the bound its check used
    When a change to "notes" sets "<key>" to "<value>"
    Then the change is rejected as "invalid" for "<key>" with the reason "<reason>"
    And the rejection gives only the <bound> <limit>

    Examples:
      | key      | value                | reason    | bound | limit                |
      | per_page | 0                    | below_min | min   | 1                    |
      | per_page | 60                   | above_max | max   | 50                   |
      | total    | 9223372036854775808  | above_max | max   | 9223372036854775807  |
      | total    | -9223372036854775809 | below_min | min   | -9223372036854775808 |

  Scenario: A choice outside its options gives the options
    When a change to "notes" sets "order" to "random"
    Then the change is rejected as "invalid" for "order" with the reason "not_option"
    And the rejection gives the options "newest, oldest"

  Scenario: A change to a secret is rejected, because a secret comes from the environment only
    When a change to "notes" sets "api_key" to "key-for-example"
    Then the change is rejected as "invalid" for "api_key" with the reason "environment_only"
    And the reason does not hold "key-for-example"

  Scenario: A change to a secret the environment sets is rejected as locked
    Given the environment holds "key-for-example" in "APP_PLUGIN_NOTES__API_KEY"
    When a change to "notes" sets "api_key" to "another-key-for-example"
    Then the change is rejected as "locked" for "api_key"
    And the rejection names the variable "APP_PLUGIN_NOTES__API_KEY"
    And the reason does not hold "key-for-example"

  Scenario Outline: An empty value is accepted for a text or a lines field
    When a change to "notes" sets "<key>" to ""
    Then the change is accepted

    Examples:
      | key   |
      | title |
      | tags  |

  Scenario Outline: An empty value is rejected for a type that has no empty value
    When a change to "notes" sets "<key>" to ""
    Then the change is rejected as "invalid" for "<key>" with the reason "<reason>"

    Examples:
      | key      | reason       |
      | per_page | not_whole    |
      | open     | not_flag     |
      | keep_for | not_duration |
      | order    | not_option   |

  Scenario: Keys the plugin does not declare are answered first, sorted
    Given the environment holds "Garden Notes" in "APP_PLUGIN_NOTES__TITLE"
    When a change to "notes" sets these settings:
      | key    | value       |
      | title  | Field Notes |
      | zebra  | stripes     |
      | colour | green       |
    Then the change is rejected as "unknown" for "colour"

  Scenario: The declared fields are answered in the manifest's order
    When a change to "notes" sets these settings:
      | key      | value  |
      | order    | random |
      | per_page | banana |
    Then the change is rejected as "invalid" for "per_page" with the reason "not_whole"
