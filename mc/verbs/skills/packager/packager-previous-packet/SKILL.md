---
name: packager-previous-packet
description: Read when the run brief contains Previous packet to compare the work and update that packet.
---

# Previous packet

The **Previous packet** part identifies an earlier packet and its submitted commit.
Use the `File` as the basis for the new packet.
Read its relevant sections through the path instead of copying the complete packet into the run context.

## Compare the work

First read **Read the reviewed change** in `~/.agents/skills/mc-packager/SKILL.md` for the current range and destination reference.
That procedure supplies `<range start>` and `<Submitted commit>` below.
Use the `Submitted commit` from **Previous packet** as `<previous commit>`.
Compare the earlier work on the current destination with these commands, in order:

    git -C /workspace/source merge-base refs/mc/destination <previous commit>
    git -C /workspace/source merge-tree --write-tree --merge-base=<previous start> <previous commit> <range start>
    git -C /workspace/source diff <tree> <Submitted commit>

The first command prints `<previous start>`, the start of the earlier reviewed range.
The second applies the earlier work to `<range start>` and prints `<tree>` on its first line.
Its status 1 means a comparison conflict and is an expected result.
The third command shows the difference between the earlier work and the current work.

A removed conflict marker shows a conflict that the work resolved.
An empty difference means that the reviewed change is the same.
For other command failures, use **A run without a packet** in mc-packager.

## Update the packet

Use **Packet result** in mc-packager for the durable packet path.
Copy the previous `File` to that path and give the copy write permission.
Update the copy. Do not write the packet from the start.
Change only the parts that the difference affects.
The updated packet describes the complete reviewed change.
It states no comparison with an earlier packet or an earlier revision, and it does not mark the parts that changed.
The updated packet has the sections of the directive's **Packet content** and follows its **Packet format**, including the page style.

- Keep each statement that the current records support.
- Correct each statement that the difference makes false or the current records no longer support.
- Add each statement and figure that the difference requires.
- Write **Criterion evidence** from the current `Evidence file` in **Latest verdict** under the directive's **Verification summary**.
- Retain prior evidence only when the difference leaves its subject unchanged.
- Keep your previous overview text when it still states the delivered change.
- Preserve **Strategic rationale** when that part applies.
