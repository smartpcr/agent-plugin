// -----------------------------------------------------------------------
// <copyright file="InactiveFiberException.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Thrown when an effect, provision or use is created on a context whose fiber is inactive
/// (already disposed).
/// </summary>
public sealed class InactiveFiberException()
    : CordisException("cannot create effect on inactive context");
