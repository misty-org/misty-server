import { describe, expect, it } from "vitest";
import { closingText, isProgressNote } from "../src/task-completion.js";

describe("progress notes", () => {
  it("tells a progress line from an answer", () => {
    expect(isProgressNote("Last: the UBalt visit page is already read and open, so I'm not reopening it. Next: I'll read UMD's Leadership Scholars page, then finish.")).toBe(true);
    expect(isProgressNote("**Last:** opened three tabs. **Have:** 4 of 10. **Next:** open the next five.")).toBe(true);
    expect(isProgressNote("**Recommendation: contact American University first.** Its PIPS scholarship is the largest. Next steps for you: call admissions.")).toBe(false);
    expect(isProgressNote("Last year the school changed its deadline. " + "Detail. ".repeat(80))).toBe(false);
    expect(isProgressNote("The popup is still showing. Let me try clicking the Cocktail Dresses link directly.")).toBe(true);
    expect(isProgressNote("PLT is done. Now I need to open JJ's House and find a deep V-neck dress.")).toBe(true);
    expect(isProgressNote("Would you like me to keep looking for more options?")).toBe(false);
    expect(isProgressNote("The cheapest flight is $212 on Delta, leaving at 7:05.")).toBe(false);
    expect(isProgressNote("## Shortlist\n- Dress: $28\n- Jogger: $25\nLet me know if you want more.")).toBe(false);
  });
});

describe("closingText", () => {
  it("keeps the fullest answer since the last tool call, not a later recap", () => {
    const call = { content: [{ type: "tool-call" }] };
    const say = (text: string) => ({ text, content: [{ type: "text" }] });
    const shortlist = "## Shortlist\n- Dress: Forest Green, $28\n- Jogger: $25\n- Boots: 6 colors\n- Cleat: $54.99, under $60";
    expect(closingText([say("Opening pages."), call, say(shortlist), say("My full answer is already provided above.")])).toBe(shortlist);
    expect(closingText([say("Early draft answer that is long " + "x".repeat(200)), call, say("Final.")])).toBe("Final.");
    expect(closingText([say("Hi! How can I help?")])).toBe("Hi! How can I help?");
  });
});
