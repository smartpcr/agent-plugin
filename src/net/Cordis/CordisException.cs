// -----------------------------------------------------------------------
// <copyright file="CordisException.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>The base class of errors raised by the Cordis runtime.</summary>
/// <param name="message">The message that describes the error.</param>
public class CordisException(string message) : Exception(message);
