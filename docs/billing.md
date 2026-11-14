# See usage and upgrade to Pro

```bash
revyl billing status                 # Plan, card, free minutes, devices, usage, limits, rates
revyl billing status --json          # The same as JSON
revyl billing add-card               # Upgrade to Pro: open Billing to add a card
revyl billing add-card --no-open     # Print the link instead
```

## Free, Pro, Enterprise

|                        | Free                          | Pro                                                 |
| ---------------------- | ----------------------------- | --------------------------------------------------- |
| Monthly fee            | None. No card.                | None                                                |
| Free device minutes    | Up to 1,200 every month       | The same, used first                                |
| After the free minutes | New work waits for next month | $0.075 per device minute on iOS and Android         |
| Also billed per minute | Drawn from the free minutes   | Web $0.05, Mac computer $0.05 |
| Devices at once        | 2                             | 10                                                  |
| Charged                | Nothing                       | $20 credit, charged when you add a card             |

**Enterprise:** let's talk. Email [support@revyl.ai](mailto:support@revyl.ai).

- **Pro:** you are on Pro once a card is on file. $20 buys about 266 device minutes on iOS or Android.
- **Device minute at $0.075:** iOS and Android, devices and builds.
- **Up to 1,200:** AI usage, web and computers draw from the same monthly allowance.
- **Auto-recharge:** $20 when credit drops under $5. Steps up as you use more. Set your own amount any time, or turn it off.
- **Monthly limit:** none unless you set one. With a limit set, auto-recharge stays at its amount.
- **Devices at once:** iOS and Android devices and Mac computers share them; Web sessions don't use them. Need more than 10? [Talk to us](https://scheduler.zoom.us/d/ijwhox6j/revyl-product-demo).
- **Organizations created earlier** keep the free minutes and rates shown on their Billing page.

Owners and admins change billing in Billing settings. On an older paid plan,
such as Starter (legacy), `revyl billing status` shows that plan's own terms.

On Free:

```text
Plan             Free
Card             none
Free minutes     85% used, 180 of 1,200 device min left, resets Dec 12
Devices at once  2
Rates            iOS Simulator     $0.075/min
                 Android Emulator  $0.075/min

With Pro         Keep running after free minutes
                 2 → 10 devices at once
                 No monthly fee
                 $20 credit, charged when you add a card
                 Auto-recharge $20 when credit drops under $5
                 Steps up as you use more. Set your own amount any time.
                 No monthly limit unless you set one

Upgrade to Pro: https://app.revyl.ai/settings/billing?add_card=1&organization_id=<org-id>&source=cli
```

On Pro:

```text
Plan             Pro
Card             on file
Free minutes     used up, resets Dec 3
This period      96 sessions, 1020 min, $31.50
Devices at once  10
                 Need more than 10? Talk to us: https://scheduler.zoom.us/d/ijwhox6j/revyl-product-demo
Credit           $12.50
Auto-recharge    $20 when credit drops under $5
                 Steps up as you use more. Set your own amount any time.
Monthly limit    none unless you set one
Rates            iOS Simulator     $0.075/min
                 Android Emulator  $0.075/min

Billing settings: https://app.revyl.ai/settings/billing
```

When a charge fails, the card row reads `last charge failed` and the last line
is the link that updates the card.

The add-card and update-card links open Billing in the organization the CLI
acts for, so they work for people in more than one organization.

`--json` carries the same facts, including `plan_name`, `charge_failed` (with
`update_card_url` when it is true), `auto_recharge.steps_up`, and
`spending_limit_set` (`false` when no monthly limit is set). `free_minutes.phone_left` and `phone_total` are set when iOS and
Android share one number, and `null` when they differ. A field Revyl did not
send is `null`.

## At a limit

- **Free minutes nearly used:** a start prints a usage notice on stderr.
  `--json` carries it as `billing_notice`.
- **Refused start:** the error names the reason and the one step that resumes
  work, with its link. `--json` carries `billing_url`, `billing_action`
  (`add_card` to upgrade to Pro, `top_up`, `update_card`, `raise_limit`,
  `request_access`, or `contact_sales`), `next_step`, and `upgrade_benefits`
  when Revyl sends it.
- **Every device busy:** the start waits for a free device instead of failing.
  `revyl test run`, `revyl device start`, and `revyl dev` print the queue
  notice on stderr when the wait begins. `revyl test run --json` and
  `revyl device start --json` carry it as `queue_notice` (`devices`,
  `action`, `billing_url`, `message`, `next_step`).
- **Computer at the device limit:** `revyl computer create` does not wait. It
  fails with `computer_limit`. On Free the error offers Pro, which runs 10
  devices at once; on Pro it names who to talk to for more. `--json` carries
  `billing_url`, `billing_action` (`add_card`, `request_access`, or
  `contact_sales`), and `next_step`.

## Coding agents

Each of these ends with an `Agents:` line. An agent recommends upgrading to
Pro when your task needs it, once, with the price and link Revyl printed. It
never adds a card, retries a refusal, or works around a limit.
