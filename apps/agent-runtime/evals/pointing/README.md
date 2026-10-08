# Pointing eval

Measures how precisely a model points at a control on a screenshot. Each turn uses the same teaching prompt (`server/internal/platform/httpapi/companion_teach_prompt.txt` and `companion_teach_turn.txt`), the same screenshot labels (`src/companion-images.ts`) and the same model route (`generateTextCall`) that the companion uses. A point counts as a hit when it lands inside the target control's box. Ledger item L7 in `docs/plans/companion-teaching-ledger.md`.

## Make fixtures

Fixtures are screenshots of your own screen, so they stay out of git. Make them on a Mac whose terminal has Screen Recording and Accessibility access:

```bash
node evals/pointing/capture-fixtures.ts --app Xcode --delay 5 --limit 25
```

Bring the named app to the front during the delay. The script captures the main display at the size the companion sends: a short side of at most 768 px and a long side of at most 1568 px. Each named control in the app's menu bar and front window becomes one fixture, a question such as "where do I click Commit?". The control's Accessibility frame becomes its target box. Run it over several apps, Misty included, to build up 30 to 50 fixtures.

A fixture is a JSON file next to its image:

```json
{
  "id": "xcode-1",
  "image": "xcode.jpg",
  "mimeType": "image/jpeg",
  "width": 1188,
  "height": 768,
  "question": "where do I click Commit?",
  "target": { "x": 300, "y": 40, "width": 60, "height": 20 }
}
```

You can also write fixtures by hand, for web pages or canvases where Accessibility has no frames.

## Run

The eval calls real models on Misty's keys (`AI_GATEWAY_API_KEY`, plus `OPENAI_API_KEY` for `openai/*`), and each fixture costs one model call per model.

```bash
MISTY_POINTING_EVAL_MODELS=openai/gpt-6-astra,anthropic/claude-sonnet-5-5 \
MISTY_POINTING_EVAL_REASONING=low \
npm run eval:pointing
```

It prints the hit rate, pointing rate, median miss distance and median latency for each model, and writes a `report-*.json` file with every reply's score. `MISTY_POINTING_EVAL_FIXTURES` points it at another fixture folder.

## Using the results

- Choose the teaching reasoning with `MISTY_COMPANION_TEACH_REASONING` on the API (the default is `low`).
- If one model clearly points better, make it the companion model in Settings.
- A miss distance that grows toward the right and bottom of the screen means the provider resized the image. Captures already stay inside the sizes providers keep (ledger L8). If this still shows up, compare the image sizes the provider reports.
