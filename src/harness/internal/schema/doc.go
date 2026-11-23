// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-12 section names it).
// The package doc is in schema.go. Step 4.2 is built with H-38 held open in the
// DH-S-4 evidence record (gap M1-G-FND-21.4): DH-T-ZOD-1 walks every registered
// config, tool, message and event type for a description on each exported
// field, and no Go-declared shape of the product carries one yet. The
// descriptions and the walk land in DH-S-12, with the file tools whose argument
// schemas are the first shipped ones the model reads.
//
//dsh:test DH-T-ZOD-1 later DH-S-12

package schema
