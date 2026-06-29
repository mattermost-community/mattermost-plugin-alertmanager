---
name: evidence-verification
description: Verify claims about external products/companies with primary sources. Use when analyzing competitors, industry trends, or making factual claims about third-party products.
---

# Evidence Verification Protocol

Use this skill when making claims about external products, companies, or industry trends. Every factual claim MUST be verifiable.

## When to Use This Skill

- Competitive product analysis (Slack, Teams, Notion, etc.)
- Industry trend claims
- Architectural comparisons with external products
- Any factual statements about third-party products/companies

## Mandatory Requirements

### 1. Cite Primary Sources ONLY
- Official API documentation (e.g., `https://api.slack.com/docs/`)
- Official product announcements (company blogs, press releases)
- Developer documentation (e.g., `https://docs.github.com/`)
- Academic papers or technical specifications
- NEVER cite: Medium posts, Reddit, unofficial blogs, paraphrases

### 2. Include URLs for Every Factual Claim
- Format: `[Claim] (Source: https://exact-url)`
- If no URL found: "**NO PRIMARY SOURCE FOUND - This is SPECULATION**"

### 3. Distinguish Facts from Inferences
- **FACT**: "Slack Canvas API requires `conversation_id` parameter" (Source: [URL])
- **INFERENCE**: "Slack chose this because..." <- No source = speculation
- Mark inferences: "**INFERENCE (not stated by vendor):** [reasoning]"
- Mark speculation: "**SPECULATION:** [hypothesis]"

### 4. Use MCP Tools for Verification BEFORE Writing
```
1. mcp__fetch-server__fetch - Get official docs directly
2. WebSearch - Find primary sources with URLs
3. mcp__gemini-cli__ask-gemini - "Find PRIMARY SOURCES with URLs for [topic]"
4. mcp__codex-native__codex - Independent verification
```

### 5. Never Attribute Reasons Without Direct Quotes
- BAD: "Microsoft deprecated Teams Wiki because channel coupling was limiting"
- GOOD: "Microsoft stated: 'limited adoption and customer preference for OneNote' (Source: [URL])"
- If vendor didn't state reason: "Vendor did not publicly state reasons. **SPECULATION:** [theory]"

### 6. Flag All Speculative Analysis
```markdown
**SPECULATION WARNING**: The following is my interpretation, NOT stated by the vendor:
- [Your analysis]
- Confidence: [Low/Medium/High]
- Alternative explanations: [What else could explain this?]
```

## Red Flags - Stop and Verify

If about to write these phrases, STOP and run verification:
- "Industry trend shows..."
- "[Company] moved away from [architecture]..."
- "[Product] failed because of [reason]..."
- "[Company] deprecated [feature] due to [reason]..."
- "Market rejected [pattern]..."
- "[Company] chose [approach] because [reason]..."
- "This represents a shift toward..."

## Architecture Classification

### Classifying as "First-Class Object" (FCO)
ONLY classify as FCO if ALL verified from API docs:
1. API allows creating entity WITHOUT required parent
2. Entity has independent permission system
3. Entity survives parent deletion
4. API schema shows `parent_id` as nullable/optional

If parent is REQUIRED, classify as SUBSERVIENT.

### Verifying "Industry Trends"
To claim a "trend", you MUST have:
- 5+ examples from different vendors
- Primary sources for EACH with URLs
- Temporal evidence (before/after dates)
- Explicit vendor statements about shifts

If <5 examples: "Among [N] products analyzed, [M] use [pattern]" NOT "Industry trend"

## Self-Check Before Publishing

- [ ] Every factual claim has a primary source URL
- [ ] Quoted reasons are actual quotes, not paraphrases
- [ ] All inferences marked: "**INFERENCE:**"
- [ ] All speculation flagged: "**SPECULATION WARNING:**"
- [ ] Architectural claims cite API docs or schemas
- [ ] "Trend" claims have 5+ examples with evidence
- [ ] Alternative explanations considered
- [ ] Confidence level stated

If any checkbox fails, add sources or mark as speculation.
