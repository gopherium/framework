Feature: Working out each plugin setting from the environment, the saved value and its default
  A downstream application lets an administrator save a plugin's settings
  and hands the saved values in beside the environment values it found.
  Each value comes from the environment, then the saved value, then the
  default, and says where it came from. A value in the environment locks
  its field, and the saved value under it waits. A saved empty text is a
  real value. A saved value its type no longer takes falls back to the
  default and is reported with its reason, so the plugin still starts. A
  secret comes from the environment only, so a saved one is never taken.
  Each field is then described for a settings form, and a secret only as
  set or not set.

  Background:
    Given the application names plugin variables under "APP_PLUGIN_"
    And the plugin "notes" declares these settings:
      | key      | type     | default | min | max | options        | label    | help                   |
      | title    | text     | Notes   |     |     |                | Title    | Shown above the notes. |
      | per_page | count    | 10      | 1   | 50  |                | Per page |                        |
      | open     | flag     | false   |     |     |                | Open     |                        |
      | keep_for | duration | 720h    |     |     |                | Keep for |                        |
      | order    | choice   | newest  |     |     | newest, oldest | Order    |                        |
      | tags     | lines    | news    |     |     |                | Tags     |                        |
      | api_key  | secret   |         |     |     |                | API key  |                        |

  Scenario Outline: A saved value wins over the default
    Given "<key>" is saved as "<value>"
    When the settings of "notes" are worked out
    Then "<key>" reads "<read>"
    And the source of "<key>" is "saved"
    And no saved value is reported as rejected

    Examples:
      | key      | value         | read         |
      | title    | Field Notes   | Field Notes  |
      | per_page | 25            | 25           |
      | open     | true          | true         |
      | keep_for | 48h           | 48h          |
      | order    | oldest        | oldest       |
      | tags     | news,, events | news, events |

  Scenario: A setting nobody saved and the environment does not hold takes its default
    When the settings of "notes" are worked out
    Then "title" reads "Notes"
    And the source of "title" is "default"

  Scenario: A value in the environment wins over a saved value and locks its field
    Given "title" is saved as "Field Notes"
    And the environment holds "Garden Notes" in "APP_PLUGIN_NOTES__TITLE"
    When the settings of "notes" are worked out
    Then "title" reads "Garden Notes"
    And the source of "title" is "environment"
    And the form shows "title" locked by "APP_PLUGIN_NOTES__TITLE"

  Scenario: A blank value in the environment counts as unset, so the saved value applies
    Given "title" is saved as "Field Notes"
    And the environment holds "  " in "APP_PLUGIN_NOTES__TITLE"
    When the settings of "notes" are worked out
    Then "title" reads "Field Notes"
    And the source of "title" is "saved"

  Scenario: A saved value under a value in the environment is not read
    Given "per_page" is saved as "banana"
    And the environment holds "25" in "APP_PLUGIN_NOTES__PER_PAGE"
    When the settings of "notes" are worked out
    Then "per_page" reads "25"
    And no saved value is reported as rejected

  Scenario Outline: A saved empty text is a real value and wins over the default
    Given "<key>" is saved as ""
    When the settings of "notes" are worked out
    Then "<key>" reads ""
    And the source of "<key>" is "saved"
    And the form shows "<key>" with the value ""

    Examples:
      | key   |
      | title |
      | tags  |

  Scenario Outline: A saved value its type no longer takes falls back to the default and is reported
    Given "<key>" is saved as "<value>"
    When the settings of "notes" are worked out
    Then "<key>" reads "<read>"
    And the source of "<key>" is "default"
    And the only saved value reported as rejected is "<key>", with the reason "<reason>"

    Examples:
      | key      | value  | read   | reason       |
      | per_page | banana | 10     | not_whole    |
      | per_page |        | 10     | not_whole    |
      | per_page | 0      | 10     | below_min    |
      | per_page | 60     | 10     | above_max    |
      | open     | maybe  | false  | not_flag     |
      | keep_for | soon   | 720h   | not_duration |
      | keep_for | -1h    | 720h   | not_positive |
      | order    | random | newest | not_option   |

  Scenario: A saved secret is never taken and never shows
    Given "api_key" is saved as "key-for-example"
    When the settings of "notes" are worked out
    Then "api_key" reads "not set"
    And the source of "api_key" is "default"
    And the only saved value reported as rejected is "api_key", with the reason "environment_only"
    And no saved value reported as rejected holds "key-for-example"
    And the form shows "api_key" as not set
    And no print, log or encoding of the settings holds "key-for-example"
    And no encoding of the form fields holds "key-for-example"

  Scenario: A saved value under a key the plugin no longer declares is left out
    Given "colour" is saved as "green"
    When the settings of "notes" are worked out
    Then no setting "colour" is worked out
    And no saved value is reported as rejected

  Scenario: Each field is described for a settings form, in the manifest's order
    Given "title" is saved as "Field Notes"
    And the environment holds "25" in "APP_PLUGIN_NOTES__PER_PAGE"
    And the environment holds "key-for-example" in "APP_PLUGIN_NOTES__API_KEY"
    When the settings of "notes" are worked out
    Then the form fields encode as:
      """
      [
        {"key": "title", "type": "text", "label": "Title", "help": "Shown above the notes.", "default": "Notes", "value": "Field Notes", "source": "saved", "locked": false, "env": "APP_PLUGIN_NOTES__TITLE"},
        {"key": "per_page", "type": "count", "label": "Per page", "min": 1, "max": 50, "default": "10", "value": "25", "source": "environment", "locked": true, "env": "APP_PLUGIN_NOTES__PER_PAGE"},
        {"key": "open", "type": "flag", "label": "Open", "default": "false", "value": "false", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__OPEN"},
        {"key": "keep_for", "type": "duration", "label": "Keep for", "default": "720h", "value": "720h", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__KEEP_FOR"},
        {"key": "order", "type": "choice", "label": "Order", "options": ["newest", "oldest"], "default": "newest", "value": "newest", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__ORDER"},
        {"key": "tags", "type": "lines", "label": "Tags", "default": "news", "value": "news", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__TAGS"},
        {"key": "api_key", "type": "secret", "label": "API key", "set": true, "source": "environment", "locked": true, "env": "APP_PLUGIN_NOTES__API_KEY"}
      ]
      """
    And no encoding of the form fields holds "key-for-example"

  Scenario Outline: A count and a flag are described in their plain form, a duration as written
    Given the environment holds "<value>" in "<variable>"
    When the settings of "notes" are worked out
    Then the form shows "<key>" with the value "<shown>"

    Examples:
      | key      | variable                   | value         | shown        |
      | per_page | APP_PLUGIN_NOTES__PER_PAGE | 020           | 20           |
      | open     | APP_PLUGIN_NOTES__OPEN     | 1             | true         |
      | keep_for | APP_PLUGIN_NOTES__KEEP_FOR | 2h            | 2h           |
      | tags     | APP_PLUGIN_NOTES__TAGS     | news,, events | news, events |
