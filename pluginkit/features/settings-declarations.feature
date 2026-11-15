Feature: Checking the settings a plugin declares
  Before a plugin registers, a downstream application checks the settings
  its plugin.json declares. Each type has its own rules for bounds, options
  and defaults, each key follows one shape, and the application reserves
  key starts of its own. A declaration outside the rules is rejected with
  the setting's place, its key and the rule it breaks. The rejection of a
  secret never quotes its default, because plugin.json is public.

  Background:
    Given the application names plugin variables under "APP_PLUGIN_"
    And the application reserves the key starts "job_" and "db_"

  Scenario: A plugin that declares one setting of each type passes the check
    Given the plugin "notes" declares these settings:
      | key      | type     | default | min | max | options        |
      | title    | text     | Notes   |     |     |                |
      | per_page | count    | 10      | 1   | 50  |                |
      | open     | flag     |         |     |     |                |
      | keep_for | duration | 720h    |     |     |                |
      | order    | choice   | newest  |     |     | newest, oldest |
      | tags     | lines    |         |     |     |                |
      | api_key  | secret   |         |     |     |                |
    When the settings of "notes" are checked
    Then the settings pass the check

  Scenario Outline: A declaration outside the rules is rejected with its reason
    Given the plugin "notes" declares <declaration>
    When the settings of "notes" are checked
    Then the check rejects them with "<reason>"

    Examples:
      | declaration                                                         | reason                                                                                           |
      | a setting "mood" of the type "colour"                               | settings[0] mood: type "colour" is not one of text, count, flag, duration, choice, secret, lines |
      | a text setting "Max__Seats"                                         | settings[0] Max__Seats: a key is lowercase words joined by one underscore                        |
      | a text setting "_title"                                             | settings[0] _title: a key is lowercase words joined by one underscore                            |
      | a text setting "job_every"                                          | settings[0] job_every: a key must not start with job_ or db_                                     |
      | a text setting "db_conns"                                           | settings[0] db_conns: a key must not start with job_ or db_                                      |
      | the text setting "title" twice                                      | settings[1] title: the key is declared twice                                                     |
      | a count "max_seats" with no default                                 | settings[0] max_seats: a count needs a default                                                   |
      | a count "max_seats" with the default "lots"                         | settings[0] max_seats: the default must be a whole number, got "lots"                            |
      | a count "max_seats" from 1 to 50 with the default "60"              | settings[0] max_seats: the default must stand at or below 50, got "60"                           |
      | a count "max_seats" from 10 to 5 with the default "7"               | settings[0] max_seats: min 10 stands above max 5                                                 |
      | a count "max_seats" from 1.5 with the default "4"                   | settings[0] max_seats: min 1.5 is not a whole number                                             |
      | a text "title" with the max 80                                      | settings[0] title: min and max apply only to a count                                             |
      | a lines "tags" with the min 1                                       | settings[0] tags: min and max apply only to a count                                              |
      | a text "title" with the options "a" and "b"                         | settings[0] title: options apply only to a choice                                                |
      | a choice "order" with no options                                    | settings[0] order: a choice needs options                                                        |
      | a choice "order" of "new " and "oldest" with the default "oldest"   | settings[0] order: an option must not start or end with a space, got "new "                      |
      | a choice "order" of "newest" and "oldest" with no default           | settings[0] order: a choice needs a default                                                      |
      | a choice "order" of "newest" and "oldest" with the default "random" | settings[0] order: the default must be one of newest, oldest, got "random"                       |
      | a duration "keep_for" with no default                               | settings[0] keep_for: a duration needs a default                                                 |
      | a duration "keep_for" with the default "soon"                       | settings[0] keep_for: the default must be a duration like 30s, got "soon"                        |
      | a flag "open" with the default "maybe"                              | settings[0] open: the default must be true or false, got "maybe"                                 |
      | a secret "api_key" with the default "key-for-example"               | settings[0] api_key: a secret takes no default, because plugin.json is public                    |
      | a text "title" that names the content type "post"                   | settings[0] title: a text setting takes no of field                                              |

  Scenario: The rejection of a secret's default never quotes it
    Given the plugin "notes" declares a secret "api_key" with the default "key-for-example"
    When the settings of "notes" are checked
    Then the check rejects them with "settings[0] api_key: a secret takes no default, because plugin.json is public"
    And the reason does not hold "key-for-example"

  Scenario Outline: A plugin with settings keeps its id clear of doubled and trailing hyphens
    Given the plugin "<id>" declares the text setting "title"
    When the settings of "<id>" are checked
    Then the check rejects them with "the id <id> must not hold two hyphens in a row or end with one when the plugin has settings"

    Examples:
      | id         |
      | notes--pro |
      | notes-     |

  Scenario: A plugin with no settings keeps any id the wiring takes
    Given the plugin "notes--pro" declares no settings
    When the settings of "notes--pro" are checked
    Then the settings pass the check
